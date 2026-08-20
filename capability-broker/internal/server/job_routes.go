package server

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/google/uuid"
)

// paid-job/v1 HTTP surface: one paid exchange on POST /v1/job.
// Transport (unary | stream | multipart) is negotiated per-request by
// ordinary HTTP mechanics against the offering's declared set. The
// idempotency layer wraps the payment middleware so a retried request
// converges on the recorded outcome without re-executing the backend
// or re-processing the payment envelope.

const (
	jobProtocol = "paid-job/v1"
	// jobInFlightTTL bounds how long a crashed exchange blocks its
	// request id before retries treat it as a failed terminal.
	jobInFlightTTL = 10 * time.Minute
	// jobRetention is the default idempotency window (paid-job §4).
	jobRetention = 24 * time.Hour
	// maxJobBodyBytes bounds buffered request/response bodies.
	maxJobBodyBytes = 64 << 20 // 64 MiB
)

func (s *Server) registerJobRoutes() {
	h := middleware.Chain(
		middleware.Recover,
		middleware.RequestID,
		middleware.Metrics,
		middleware.Headers,
	)(s.jobIdempotency(
		middleware.Chain(middleware.Payment(s.payment, s.lookupSpec, s.opts.InterimDebit, s.receiptSink))(
			http.HandlerFunc(s.handleJob))))
	s.mux.Handle("POST /v1/job", h)
}

// jobCapability finds the paid-job capability tuple.
func (s *Server) jobCapability(capID, offID string) *config.Capability {
	cfg := s.currentConfig()
	for i := range cfg.Capabilities {
		c := &cfg.Capabilities[i]
		if c.ID == capID && c.OfferingID == offID &&
			strings.HasPrefix(c.Protocol, "paid-job/") && c.Job != nil {
			return c
		}
	}
	return nil
}

// negotiateTransport picks the transport per paid-job §2.
func negotiateTransport(r *http.Request) string {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return "multipart"
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return "stream"
	}
	return "unary"
}

// ---------------------------------------------------------------------------
// idempotency

// jobIdemStore abstracts the request-id record store. Durable when the
// broker has a configured state store; in-process otherwise (logged —
// spec conformance requires the durable form).
type jobIdemStore interface {
	Begin(requestID string, fingerprint []byte, jobID string, deadline time.Time) (*sessionstore.JobRecord, bool, error)
	Finish(requestID string, status int, workUnits uint64, unit string) error
}

type boltJobIdem struct{ store *sessionstore.Store }

func (b *boltJobIdem) Begin(id string, fp []byte, jobID string, dl time.Time) (*sessionstore.JobRecord, bool, error) {
	return b.store.JobBegin(id, fp, jobID, dl)
}
func (b *boltJobIdem) Finish(id string, status int, units uint64, unit string) error {
	return b.store.JobFinish(id, status, units, unit)
}

type memJobIdem struct {
	mu   sync.Mutex
	recs map[string]*sessionstore.JobRecord
}

func newMemJobIdem() *memJobIdem { return &memJobIdem{recs: map[string]*sessionstore.JobRecord{}} }

func (m *memJobIdem) Begin(id string, fp []byte, jobID string, dl time.Time) (*sessionstore.JobRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.recs[id]; ok {
		if !bytes.Equal(existing.Fingerprint, fp) {
			return nil, false, sessionstore.ErrRequestIDReuse
		}
		cp := *existing
		return &cp, false, nil
	}
	rec := &sessionstore.JobRecord{
		RequestID: id, JobID: jobID, Fingerprint: bytes.Clone(fp),
		State: sessionstore.JobInFlight, Deadline: dl, CreatedAt: time.Now().UTC(),
	}
	m.recs[id] = rec
	cp := *rec
	return &cp, true, nil
}

func (m *memJobIdem) Finish(id string, status int, units uint64, unit string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.recs[id]
	if !ok {
		return sessionstore.ErrNotFound
	}
	rec.State = sessionstore.JobTerminal
	rec.Status = status
	rec.WorkUnits = units
	rec.Unit = unit
	rec.EndedAt = time.Now().UTC()
	return nil
}

// jobFingerprint binds a request id to its content: capability,
// offering, payment envelope, and content length. (Body-hash matching
// would require buffering multipart uploads twice; the envelope hash
// already binds the payment, which is the load-bearing part.)
func jobFingerprint(r *http.Request) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%d",
		r.Header.Get(livepeerheader.Capability),
		r.Header.Get(livepeerheader.Offering),
		r.Header.Get(livepeerheader.Payment),
		r.ContentLength)
	return h.Sum(nil)
}

// jobIdempotency wraps the paid chain per paid-job §4.
func (s *Server) jobIdempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.Header.Get(livepeerheader.Protocol); p != jobProtocol {
			livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported,
				livepeerheader.ErrProtocolUnsupported, "this endpoint serves "+jobProtocol+"; got "+p)
			return
		}
		capID := r.Header.Get(livepeerheader.Capability)
		offID := r.Header.Get(livepeerheader.Offering)
		c := s.jobCapability(capID, offID)
		if c == nil {
			livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed,
				"no paid-job offering "+capID+"/"+offID)
			return
		}
		// Transport refusal happens before any payment side effects.
		transport := negotiateTransport(r)
		declared := false
		for _, tr := range c.Job.Transports {
			if tr == transport {
				declared = true
				break
			}
		}
		if !declared {
			observability.RecordJobExchange(transport, "refused")
			livepeerheader.WriteError(w, http.StatusBadRequest,
				livepeerheader.ErrTransportUnsupported,
				"offering does not declare transport "+transport)
			return
		}

		requestID := r.Header.Get(livepeerheader.RequestID)
		jobID := "job_" + uuid.NewString()
		rec, created, err := s.jobIdem.Begin(requestID, jobFingerprint(r), jobID, time.Now().Add(jobInFlightTTL))
		if err != nil {
			if errors.Is(err, sessionstore.ErrRequestIDReuse) {
				livepeerheader.WriteError(w, http.StatusBadRequest,
					livepeerheader.ErrRequestIDReuse,
					"request id replayed with different capability, offering, envelope, or length")
				return
			}
			livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, err.Error())
			return
		}
		if !created {
			switch {
			case rec.State == sessionstore.JobTerminal:
				// Replay the recorded outcome: status + claim headers,
				// no backend re-execution, no second debit.
				w.Header().Set(livepeerheader.JobID, rec.JobID)
				w.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(rec.WorkUnits, 10))
				w.Header().Set(livepeerheader.WorkUnitName, rec.Unit)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(rec.Status)
				fmt.Fprintf(w, `{"replayed":true,"job_id":%q}`, rec.JobID)
				observability.RecordJobExchange(transport, "replayed")
				return
			case time.Now().After(rec.Deadline):
				// Crash leftover: converge on a failed terminal.
				_ = s.jobIdem.Finish(requestID, http.StatusInternalServerError, 0, c.WorkUnit.Name)
				w.Header().Set(livepeerheader.JobID, rec.JobID)
				w.Header().Set(livepeerheader.WorkUnits, "0")
				w.Header().Set(livepeerheader.WorkUnitName, c.WorkUnit.Name)
				livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
					"original exchange did not complete")
				return
			default:
				livepeerheader.WriteError(w, http.StatusConflict, livepeerheader.ErrJobInFlight,
					"request id has an exchange in flight; retry after it completes")
				return
			}
		}

		w.Header().Set(livepeerheader.JobID, rec.JobID)
		w.Header().Set(livepeerheader.WorkUnitName, c.WorkUnit.Name)
		jrec := &jobRecorder{ResponseWriter: w}
		next.ServeHTTP(jrec, r)
		switch st := jrec.status(); {
		case st < 400:
			observability.RecordJobExchange(transport, "ok")
		case st < 500:
			observability.RecordJobExchange(transport, "client_error")
		default:
			observability.RecordJobExchange(transport, "backend_error")
		}
		if err := s.jobIdem.Finish(requestID, jrec.status(), jrec.units(), c.WorkUnit.Name); err != nil {
			log.Printf("warning: job idempotency finish failed request_id=%s: %v", requestID, err)
		}
	})
}

// jobRecorder captures the terminal status and the Work-Units claim
// (which streams set as a trailer, i.e. into Header() after the body).
type jobRecorder struct {
	http.ResponseWriter
	code int
}

func (j *jobRecorder) WriteHeader(code int) {
	if j.code == 0 {
		j.code = code
	}
	j.ResponseWriter.WriteHeader(code)
}

func (j *jobRecorder) Write(b []byte) (int, error) {
	if j.code == 0 {
		j.code = http.StatusOK
	}
	return j.ResponseWriter.Write(b)
}

func (j *jobRecorder) Flush() {
	if f, ok := j.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (j *jobRecorder) status() int {
	if j.code == 0 {
		return http.StatusOK
	}
	return j.code
}

func (j *jobRecorder) units() uint64 {
	if h := j.Header().Get(livepeerheader.WorkUnits); h != "" {
		if n, err := strconv.ParseUint(h, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// transport handler (runs inside the Payment middleware)

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	capID := r.Header.Get(livepeerheader.Capability)
	offID := r.Header.Get(livepeerheader.Offering)
	group, ok := s.groupFor(capID, offID)
	if !ok {
		livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed,
			"no backend group for "+capID+"/"+offID)
		return
	}
	c, err := s.selectBackend(group)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusServiceUnavailable, livepeerheader.ErrCapacityExhausted,
			"backend selection: "+err.Error())
		return
	}
	release, ok := s.reserveBackend(c)
	if !ok {
		w.Header().Set(livepeerheader.Backoff, "5")
		livepeerheader.WriteError(w, http.StatusServiceUnavailable, livepeerheader.ErrCapacityExhausted,
			"backend at capacity")
		return
	}
	defer release()

	extractor, err := s.extractors.Build(c.WorkUnit.Extractor)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"extractor: "+err.Error())
		return
	}

	start := time.Now()
	reqBody, err := io.ReadAll(io.LimitReader(r.Body, maxJobBodyBytes))
	if err != nil {
		livepeerheader.WriteBadRequest(w, "read request body: "+err.Error())
		return
	}
	outHeaders := backend.StripLivepeerHeaders(r.Header)
	if err := backend.NewAuthApplier(s.secrets).Apply(outHeaders, c.Backend.Auth); err != nil {
		livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
			"inject backend auth: "+err.Error())
		return
	}
	resp, err := s.backend.Forward(r.Context(), backend.ForwardRequest{
		URL:     c.Backend.URL,
		Method:  r.Method,
		Headers: outHeaders,
		Body:    bytes.NewReader(reqBody),
	})
	if err != nil {
		w.Header().Set(livepeerheader.WorkUnits, "0")
		livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
			"backend forward: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if negotiateTransport(r) == "stream" {
		s.streamJobResponse(w, r, resp, extractor, reqBody, start)
		return
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxJobBodyBytes))
	if err != nil {
		w.Header().Set(livepeerheader.WorkUnits, "0")
		livepeerheader.WriteError(w, http.StatusBadGateway, livepeerheader.ErrBackendUnavailable,
			"read backend body: "+err.Error())
		return
	}
	units := s.extractUnits(r, extractor, reqBody, respBody, resp, start)
	copyBackendHeaders(w, resp)
	w.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(units, 10))
	if resp.StatusCode >= 500 {
		w.Header().Set(livepeerheader.Error, livepeerheader.ErrBackendUnavailable)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

// streamJobResponse pipes the backend body through while buffering a
// bounded copy for the extractor; the claim arrives as an advertised
// trailer (paid-job §3.2).
func (s *Server) streamJobResponse(w http.ResponseWriter, r *http.Request, resp *http.Response,
	extractor extractors.Extractor, reqBody []byte, start time.Time) {

	copyBackendHeaders(w, resp)
	// Chunked encoding is required for trailers: a copied Content-Length
	// would suppress them (and is wrong for a live stream anyway).
	w.Header().Del("Content-Length")
	w.Header().Add("Trailer", livepeerheader.WorkUnits)
	w.WriteHeader(resp.StatusCode)

	var buf bytes.Buffer
	flusher, _ := w.(http.Flusher)
	tee := io.TeeReader(io.LimitReader(resp.Body, maxJobBodyBytes), &buf)
	chunk := make([]byte, 32<<10)
	for {
		n, err := tee.Read(chunk)
		if n > 0 {
			if _, werr := w.Write(chunk[:n]); werr != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
	units := s.extractUnits(r, extractor, reqBody, buf.Bytes(), resp, start)
	w.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(units, 10))
}

func (s *Server) extractUnits(r *http.Request, extractor extractors.Extractor,
	reqBody, respBody []byte, resp *http.Response, start time.Time) uint64 {
	units, err := extractor.Extract(r.Context(), &extractors.Request{
		Method:  r.Method,
		Body:    reqBody,
		Headers: r.Header,
	}, &extractors.Response{
		Status:   resp.StatusCode,
		Body:     respBody,
		Headers:  resp.Header,
		Trailers: resp.Trailer,
		Duration: time.Since(start),
	})
	if err != nil {
		log.Printf("warning: extractor %s failed: %v", extractor.Name(), err)
		return 0
	}
	return units
}

func copyBackendHeaders(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		if strings.HasPrefix(k, "Livepeer-") || isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
}

func isHopByHop(k string) bool {
	switch http.CanonicalHeaderKey(k) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}
