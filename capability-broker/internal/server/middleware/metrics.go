package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/observability"
)

// Metrics is a paid-route middleware that records request observability:
//
//   - Increments livepeer_mode_requests_total by (capability, offering, outcome).
//   - Observes livepeer_mode_request_duration_seconds.
//   - Sums livepeer_mode_work_units_total.
//   - Emits one structured log line per request including request_id, status,
//     and Livepeer-Error code when present.
//
// Outcome is derived from the response: the Livepeer-Error header value if
// set, "success" for 2xx without an error code, "other" otherwise.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		duration := time.Since(start)

		capID := r.Header.Get(livepeerheader.Capability)
		offID := r.Header.Get(livepeerheader.Offering)
		errCode := rec.Header().Get(livepeerheader.Error)
		status := rec.statusCode
		if status == 0 {
			// next.ServeHTTP wrote nothing; treat as 200 per Go's default.
			status = http.StatusOK
		}

		outcome := outcomeFor(status, errCode)

		// Read units from the recorder snapshot (set at WriteHeader time);
		// fall back to the response-header map for trailer-based modes
		// like http-stream@v0 where the value lands after the body.
		workUnits := rec.workUnits
		if workUnits == 0 {
			if h := rec.Header().Get(livepeerheader.WorkUnits); h != "" {
				if n, err := strconv.ParseUint(h, 10, 64); err == nil {
					workUnits = n
				}
			}
		}

		observability.RecordRequest(capID, offID, outcome, duration.Seconds(), workUnits)

		slog.Info("paid request",
			"request_id", RequestIDFromContext(r.Context()),
			"capability", capID,
			"offering", offID,
			"mode", r.Header.Get(livepeerheader.Mode),
			"status", status,
			"livepeer_error", errCode,
			"work_units", workUnits,
			"duration", duration,
			"outcome", outcome,
		)
	})
}

func outcomeFor(status int, errCode string) string {
	if errCode != "" {
		return errCode
	}
	if status >= 200 && status < 300 {
		return "success"
	}
	return "other"
}
