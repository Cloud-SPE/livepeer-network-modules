package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

// maxDeferredBytes bounds what a deferred response will hold before it
// gives up and commits. The unary job path already buffers its whole body
// to measure it, so this is a second copy of something bounded rather
// than a new unbounded cost — but "bounded by 64 MiB" is not a promise
// worth making per in-flight request, and a response this large is not
// the JSON exchange the deferral exists for.
const maxDeferredBytes = 8 << 20

// responseRecorder is an http.ResponseWriter wrapper that snapshots the
// Livepeer-Work-Units header at WriteHeader time. The Payment middleware
// uses it to read the value the mode driver set, so it can reconcile with
// the payment-daemon.
//
// It can also DEFER the response. A unary handler commits its headers
// before the middleware runs the final debit, so Livepeer-Work-Units
// named a measurement the ledger had not yet accepted — and might refuse.
// Deferring holds the status, headers and body until the debit has
// resolved, so the header states what was actually debited.
//
// Deferral ends the moment the handler proves it is streaming, because a
// stream cannot be held: the first Flush, a Hijack, a declared trailer,
// or simply too many bytes all commit immediately and restore the
// pass-through behaviour. A streamed response corrects its own claim in
// the trailer it declares, which is why it never needed this.
type responseRecorder struct {
	http.ResponseWriter
	wroteHeader bool
	statusCode  int
	workUnits   uint64

	deferring bool
	committed bool
	body      bytes.Buffer
	overflow  bool
}

// deferResponse holds the response until commit. Callers must ensure
// commit runs on every path.
func (r *responseRecorder) deferResponse() { r.deferring = true }

// deferred reports whether the response is still being held, i.e. the
// headers can still be corrected.
func (r *responseRecorder) deferred() bool { return r.deferring && !r.committed }

func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.statusCode = code
	if h := r.Header().Get(livepeerheader.WorkUnits); h != "" {
		if n, err := strconv.ParseUint(h, 10, 64); err == nil {
			r.workUnits = n
		}
	}
	// A declared trailer means the handler intends to correct its own
	// claim after the body, which is the streamed path. Holding that
	// response would stall a live stream, and correcting a header it
	// already promised to send as a trailer would be two answers to one
	// question.
	if r.deferring && r.Header().Get("Trailer") != "" {
		r.deferring = false
	}
	if r.deferring {
		return
	}
	r.committed = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if r.deferred() {
		if r.body.Len()+len(b) > maxDeferredBytes {
			// Too large to hold. Commit what we have and stream the
			// rest — the header will be the handler's measurement
			// rather than the debited figure, which is the behaviour
			// this whole mechanism exists to improve. Recorded so the
			// caller can say so rather than quietly claim otherwise.
			r.overflow = true
			r.commit()
			return r.ResponseWriter.Write(b)
		}
		return r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

// commit flushes a deferred response: headers first, then the body held
// back. Safe to call more than once, and a no-op when nothing was
// deferred.
func (r *responseRecorder) commit() {
	if !r.deferring || r.committed {
		return
	}
	r.committed = true
	code := r.statusCode
	if code == 0 {
		code = http.StatusOK
	}
	r.ResponseWriter.WriteHeader(code)
	if r.body.Len() > 0 {
		_, _ = r.ResponseWriter.Write(r.body.Bytes())
		r.body.Reset()
	}
}

// Hijack passes through to the underlying ResponseWriter if it supports
// http.Hijacker. Required so the ws-realtime mode driver can upgrade the
// connection through the middleware chain. Marks the recorder as
// "headers written" with status 101 so post-handler observability sees a
// reasonable value.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not implement http.Hijacker")
	}
	// A hijacked connection is the caller's; nothing can be held back
	// on it.
	r.deferring = false
	r.committed = true
	conn, brw, err := h.Hijack()
	if err == nil && !r.wroteHeader {
		r.wroteHeader = true
		r.statusCode = http.StatusSwitchingProtocols
	}
	return conn, brw, err
}

// Flush passes through to the underlying ResponseWriter if it supports
// http.Flusher. Required for streaming modes (http-stream@v0).
func (r *responseRecorder) Flush() {
	// A handler that flushes is streaming, and a stream held until the
	// debit resolves is not a stream.
	if r.deferred() {
		r.commit()
	}
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
