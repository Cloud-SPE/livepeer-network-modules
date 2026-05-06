package registry

import "net/http"

// HealthzHandler returns 200 OK if the broker process is alive enough to
// handle requests. Used for kubernetes-style liveness probes; orthogonal to
// /registry/health (which reports per-capability backend availability).
func HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}
}
