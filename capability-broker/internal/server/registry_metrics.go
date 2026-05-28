package server

import (
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
)

// instrumentRegistryScrape wraps an unpaid registry endpoint handler to record
// scrape count, duration, terminal HTTP status, and response payload size on
// the broker's /metrics surface (livepeer_broker_registry_*). The registry
// endpoints sit outside the paid middleware chain, so they would otherwise be
// invisible to operators.
func instrumentRegistryScrape(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &scrapeRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		observability.RecordRegistryScrape(endpoint, rec.status, time.Since(start).Seconds(), rec.bytes)
	}
}

// scrapeRecorder captures the terminal status code and the number of bytes
// written so the scrape metrics can label by outcome and observe payload size.
type scrapeRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (s *scrapeRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *scrapeRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}
