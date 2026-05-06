package middleware

import (
	"log"
	"net/http"
	"runtime/debug"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// Recover catches panics in downstream handlers and returns 500 with
// Livepeer-Error: internal_error. Stacks are logged but never returned to the
// caller.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic in handler: %v\n%s", rec, debug.Stack())
				// Best-effort: response may already be partially written.
				livepeerheader.WriteError(w, http.StatusInternalServerError,
					livepeerheader.ErrInternalError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
