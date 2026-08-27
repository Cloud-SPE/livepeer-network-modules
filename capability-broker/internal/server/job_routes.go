package server

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
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
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
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
	// defaultJobRetention is how long terminal evidence is kept.
	//
	// The rule is operational, not chain-derived:
	//
	//   retention > conservative-charge deadline
	//             + consumer outage/recovery window
	//             + scheduler and clock margin
	//
	// An earlier version derived it from the envelope's spendable life.
	// That is not implementable: governance can raise
	// ticketValidityPeriod and revive tickets, so maximum spendable life
	// has no finite bound and a retention window derived from it has no
	// value to compute.
	//
	// What retention actually has to outlast is the consumer's own
	// reconciliation: the point at which it gives up waiting and applies
	// a conservative charge, plus however long it might itself be down,
	// plus slack for the sweeps at both ends. 96h is that sum for a
	// consumer with a ~48h charge deadline and a 24h outage target.
	// Operators serving a consumer with a longer window raise
	// session_store.job_retention.
	//
	// Retention starts at TERMINAL state for a settlement and at
	// observed_at for a non-admission record. In-flight and
	// accounting-pending records are never evicted as terminal evidence;
	// the first is closed out, the second is still moving.
	defaultJobRetention = 96 * time.Hour
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
		middleware.Chain(middleware.Payment(s.payment, s.lookupSpec, s.opts.InterimDebit, s.receiptSink,
			func(rec *pb.SettlementRecord) (string, error) {
				// Both protocols emit the same signed envelope, so a
				// clearinghouse verifies settlement with one code path.
				return settlement.Encode(rec, s.settlementSigner)
			},
			s.allocDebitSeq))(
			http.HandlerFunc(s.handleJob))))
	s.mux.Handle("POST /v1/job", h)
}

// jobCapability finds the paid-job tuple being served, from either
// grammar: a configured capability, or a frozen offer with its declared
// axes (plan 0043 item 10). Nil means the broker does not advertise it
// at all — distinct from advertising it with nobody currently able to
// serve it, which is a 503 further down.
func (s *Server) jobCapability(capID, offID string) *config.Capability {
	group, ok := s.offerGroupFor(capID, offID)
	if !ok || group.Published == nil ||
		!strings.HasPrefix(group.Published.Protocol, "paid-job/") || group.Published.Job == nil {
		return nil
	}
	return group.Published
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
	Finish(requestID string, status int, workUnits uint64, unit string, bodyDigest []byte, settlement string) error
	// FinishPendingAccounting records a delivered exchange whose debit
	// is still outstanding, so it can be retried and settled later.
	FinishPendingAccounting(requestID string, status int, workUnits uint64, unit string,
		bodyDigest []byte, pd *middleware.PendingDebit) error
	ByJobID(jobID string) (*sessionstore.JobRecord, error)
	// ByRequestID resolves the id the CONSUMER issued. Job records are
	// keyed by it, so this is the cheap lookup — it was simply never
	// exposed, which left a clearinghouse holding only its own id unable
	// to find an exchange the broker had settled.
	ByRequestID(requestID string) (*sessionstore.JobRecord, error)
}

type boltJobIdem struct{ store *sessionstore.Store }

func (b *boltJobIdem) Begin(id string, fp []byte, jobID string, dl time.Time) (*sessionstore.JobRecord, bool, error) {
	return b.store.JobBegin(id, fp, jobID, dl)
}
func (b *boltJobIdem) Finish(id string, status int, units uint64, unit string, bodyDigest []byte, settlement string) error {
	return b.store.JobFinish(id, status, units, unit, bodyDigest, settlement)
}

func (b *boltJobIdem) FinishPendingAccounting(id string, status int, units uint64, unit string,
	bodyDigest []byte, pd *middleware.PendingDebit) error {
	return b.store.JobFinishPendingAccounting(id, status, units, unit, bodyDigest, toStorePending(pd))
}

// toStorePending converts the middleware's in-flight shape to the
// durable one. The two are separate types on purpose: the middleware
// package has no business importing the store, and the durable record
// has to survive a restart, so its big.Int becomes a decimal string.
func toStorePending(pd *middleware.PendingDebit) *sessionstore.PendingDebit {
	if pd == nil {
		return nil
	}
	funded := "0"
	if pd.FundedValueWei != nil {
		funded = pd.FundedValueWei.String()
	}
	return &sessionstore.PendingDebit{
		Sender:            append([]byte(nil), pd.Sender...),
		WorkID:            pd.WorkID,
		DebitSeq:          pd.DebitSeq,
		Units:             pd.Units,
		DebitedUnits:      pd.DebitedUnits,
		PaymentBytes:      append([]byte(nil), pd.PaymentBytes...),
		FundedValueWei:    funded,
		ActualUnits:       pd.ActualUnits,
		WorkUnitName:      pd.WorkUnitName,
		TerminationReason: pd.TerminationReason,
		JobID:             pd.JobID,
		RequestID:         pd.RequestID,
		IssuedAt:          pd.IssuedAt,
		// Due immediately: the first retry should not wait out a backoff
		// the exchange has not earned yet.
		NextAttemptAt: time.Now().UTC(),
		FirstFailedAt: time.Now().UTC(),
	}
}
func (b *boltJobIdem) ByJobID(jobID string) (*sessionstore.JobRecord, error) {
	return b.store.JobByID(jobID)
}

func (b *boltJobIdem) ByRequestID(requestID string) (*sessionstore.JobRecord, error) {
	return b.store.JobByRequestID(requestID)
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

func (m *memJobIdem) ByRequestID(requestID string) (*sessionstore.JobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.recs[requestID]; ok {
		cp := *rec
		return &cp, nil
	}
	return nil, sessionstore.ErrNotFound
}

func (m *memJobIdem) ByJobID(jobID string) (*sessionstore.JobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range m.recs {
		if rec.JobID == jobID {
			cp := *rec
			return &cp, nil
		}
	}
	return nil, sessionstore.ErrNotFound
}

func (m *memJobIdem) FinishPendingAccounting(id string, status int, units uint64, unit string,
	bodyDigest []byte, pd *middleware.PendingDebit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.recs[id]
	if !ok {
		return sessionstore.ErrNotFound
	}
	rec.State = sessionstore.JobAccountingPending
	rec.Status = status
	rec.WorkUnits = units
	rec.Unit = unit
	rec.BodyDigest = bytes.Clone(bodyDigest)
	rec.EndedAt = time.Now().UTC()
	rec.Pending = toStorePending(pd)
	return nil
}

func (m *memJobIdem) Finish(id string, status int, units uint64, unit string, bodyDigest []byte, settlement string) error {
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
	rec.BodyDigest = bytes.Clone(bodyDigest)
	rec.Settlement = settlement
	rec.EndedAt = time.Now().UTC()
	return nil
}

// jobEnvelopeFingerprint binds a request id to what is knowable before
// the body streams: capability, offering, and the payment envelope.
//
// It used to include ContentLength and call itself the content
// fingerprint, which let a retry that reused the id and the envelope but
// changed the body to one of equal length receive the first exchange's
// recorded outcome — a wrong answer rather than request_id_reuse. The
// body is bound separately, by digest, because it cannot be hashed until
// it has been read and the record has to exist before then.
func jobEnvelopeFingerprint(r *http.Request) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s",
		r.Header.Get(livepeerheader.Capability),
		r.Header.Get(livepeerheader.Offering),
		r.Header.Get(livepeerheader.Payment))
	return h.Sum(nil)
}

// hashingBody streams the request body through a digest on its way to
// the backend, so binding the body costs no buffering — which is what
// made body-hashing look impractical for multipart uploads.
type hashingBody struct {
	io.ReadCloser
	h hash.Hash
}

func newHashingBody(rc io.ReadCloser) *hashingBody {
	return &hashingBody{ReadCloser: rc, h: sha256.New()}
}

func (b *hashingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.h.Write(p[:n])
	}
	return n, err
}

func (b *hashingBody) digest() []byte { return b.h.Sum(nil) }

// drainDigest reads a body to its end for its digest alone, without
// forwarding it anywhere. This is the replay path: a retry has to prove
// it carries the same content, and proving it costs a read rather than
// an execution.
func drainDigest(rc io.ReadCloser) []byte {
	h := sha256.New()
	if rc != nil {
		_, _ = io.Copy(h, rc)
	}
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
		rec, created, err := s.jobIdem.Begin(requestID, jobEnvelopeFingerprint(r), jobID, time.Now().Add(jobInFlightTTL))
		if err != nil {
			if errors.Is(err, sessionstore.ErrRequestIDReuse) {
				livepeerheader.WriteError(w, http.StatusBadRequest,
					livepeerheader.ErrRequestIDReuse,
					"request id replayed with a different capability, offering, or payment envelope")
				return
			}
			livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, err.Error())
			return
		}
		if !created {
			switch {
			case rec.State == sessionstore.JobTerminal:
				// The envelope matched; the body still has to. Draining
				// the retry for its digest costs a read and proves the
				// content, where trusting the envelope alone would let a
				// changed body receive the first exchange's outcome.
				if len(rec.BodyDigest) > 0 && !bytes.Equal(rec.BodyDigest, drainDigest(r.Body)) {
					livepeerheader.WriteError(w, http.StatusBadRequest,
						livepeerheader.ErrRequestIDReuse,
						"request id replayed with a different body")
					return
				}
				// Replay the recorded outcome: status + claim headers,
				// no backend re-execution, no second debit.
				w.Header().Set(livepeerheader.JobID, rec.JobID)
				w.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(rec.WorkUnits, 10))
				w.Header().Set(livepeerheader.WorkUnitName, rec.Unit)
				// The recorded claim includes the settlement. Replay
				// carried the status, the job id and the units but
				// dropped this, so a caller retrying an exchange whose
				// response it lost got back everything EXCEPT the
				// evidence it retried for. Idempotent means the same
				// answer, not a similar one.
				if rec.Settlement != "" {
					w.Header().Set(livepeerheader.Settlement, rec.Settlement)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(rec.Status)
				fmt.Fprintf(w, `{"replayed":true,"job_id":%q}`, rec.JobID)
				observability.RecordJobExchange(transport, "replayed")
				return
			case time.Now().After(rec.Deadline):
				// Crash leftover: converge on a failed terminal.
				_ = s.jobIdem.Finish(requestID, http.StatusInternalServerError, 0, c.WorkUnit.Name, nil, "")
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
		body := newHashingBody(r.Body)
		r.Body = body
		jrec := &jobRecorder{ResponseWriter: w}
		// A slot for the payment layer to report a debit that did not
		// land. It sits inside this layer but the durable record lives
		// here, so the failure has to travel outward.
		ctx, pendingSlot := middleware.WithPendingDebitSlot(r.Context())
		next.ServeHTTP(jrec, r.WithContext(ctx))
		switch st := jrec.status(); {
		case st < 400:
			observability.RecordJobExchange(transport, "ok")
		case st < 500:
			observability.RecordJobExchange(transport, "client_error")
		default:
			observability.RecordJobExchange(transport, "backend_error")
		}
		if pd := pendingSlot.Get(); pd != nil {
			// Delivered but unsettled. The outcome is recorded so a
			// replay still returns it; the record stays non-terminal so
			// nothing reports the accounting as done while a debit is
			// outstanding.
			if err := s.jobIdem.FinishPendingAccounting(requestID, jrec.status(), jrec.units(),
				c.WorkUnit.Name, body.digest(), pd); err != nil {
				log.Printf("warning: job pending-accounting record failed request_id=%s: %v",
					requestID, err)
			}
			return
		}
		if err := s.jobIdem.Finish(requestID, jrec.status(), jrec.units(), c.WorkUnit.Name,
			body.digest(), jrec.settlement()); err != nil {
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

// settlement returns the envelope the payment middleware emitted, header
// or trailer. Persisting it is what lets a caller that could not read a
// trailer ask for it later.
func (j *jobRecorder) settlement() string {
	return j.Header().Get(livepeerheader.Settlement)
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
	// A non-2xx backend produced no billable output, so the claim is
	// zero regardless of what the extractor would compute (paid-job §5).
	//
	// Leaving it to the extractor made the rule an accident of
	// configuration: `openai-usage` finds no usage object in an error
	// body and reaches zero on its own, but a request-derived extractor
	// — `request-formula` over an image `n`, a per-request constant —
	// returns its full count for a request the backend never served.
	// The unit that decides whether work was billable cannot be the one
	// that never looked at the outcome.
	//
	// Streaming is deliberately not covered here: a stream that failed
	// partway still delivered client-visible output, and how partial is
	// billable is what the extractor declaration exists to decide.
	var units uint64
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		units = s.extractUnits(r, extractor, reqBody, respBody, resp, start)
	}
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
	// Declared here rather than in the payment middleware, which cannot
	// know the transport yet: this is the one path guaranteed to be
	// chunked, and a trailer advertised on a Content-Length response is
	// dropped by net/http without a word.
	w.Header().Add("Trailer", livepeerheader.Settlement)
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

// jobRetention returns the configured retention, or the default.
//
// See defaultJobRetention for why this outlasts the payment envelope's
// expiry rather than just the idempotency window.
func (s *Server) jobRetention() time.Duration {
	if s.cfg != nil && s.cfg.SessionStore.JobRetention > 0 {
		return s.cfg.SessionStore.JobRetention
	}
	return defaultJobRetention
}

// minEvidenceRetention is the floor below which the broker warns.
//
// Not derived from the chain — see defaultJobRetention for why that is
// not a computable quantity. It is a conservative stand-in for a
// consumer's reconciliation window, and an operator who knows their
// consumer's actual deadline should set job_retention from that rather
// than from this.
const minEvidenceRetention = 72 * time.Hour
