package middleware

import (
	"encoding/json"
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
				w.Header().Set(livepeerheader.Error, livepeerheader.ErrInternalError)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":   livepeerheader.ErrInternalError,
					"message": "internal error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
