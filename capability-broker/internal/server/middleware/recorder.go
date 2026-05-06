package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// responseRecorder is an http.ResponseWriter wrapper that snapshots the
// Livepeer-Work-Units header at WriteHeader time. The Payment middleware
// uses it to read the value the mode driver set, so it can reconcile with
// the payment-daemon.
type responseRecorder struct {
	http.ResponseWriter
	wroteHeader bool
	statusCode  int
	workUnits   uint64
}

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
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
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
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
