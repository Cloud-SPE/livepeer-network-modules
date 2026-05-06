package middleware

import (
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
