package metrics

import "time"

// ObserveScrape records one broker-scrape outcome.
func (r *Registry) ObserveScrape(broker, outcome string, dur time.Duration) {
	r.ScrapeTotal.WithLabelValues(broker, outcome).Inc()
	r.ScrapeDuration.WithLabelValues(broker).Observe(dur.Seconds())
}

// ObserveBrokerCounts updates the cardinality-1 broker gauges.
func (r *Registry) ObserveBrokerCounts(known, healthy int) {
	r.KnownBrokers.Set(float64(known))
	r.BrokersHealthy.Set(float64(healthy))
}

// ObserveCandidateBuild records a candidate-build outcome.
func (r *Registry) ObserveCandidateBuild(outcome string, dur time.Duration) {
	r.CandidateBuildTotal.WithLabelValues(outcome).Inc()
	r.CandidateBuildDuration.Observe(dur.Seconds())
}

// ObserveUpload records a signed-upload outcome.
func (r *Registry) ObserveUpload(outcome string, _ time.Duration) {
	r.SignedUploadsTotal.WithLabelValues(outcome).Inc()
}

// ObservePublish records a publish-attempt outcome.
func (r *Registry) ObservePublish(outcome string) {
	r.PublishesTotal.WithLabelValues(outcome).Inc()
}

// ObserveVerifyDuration records the wall-clock verify duration.
func (r *Registry) ObserveVerifyDuration(dur time.Duration) {
	r.SignedVerifyDuration.Observe(dur.Seconds())
}

// SetManifestState updates the published-manifest gauges.
func (r *Registry) SetManifestState(ageSeconds float64, tupleCount int) {
	r.ManifestAgeSeconds.Set(ageSeconds)
	r.CapabilityTuples.Set(float64(tupleCount))
}

// SetDriftCount updates the per-kind drift gauge.
func (r *Registry) SetDriftCount(kind string, count int) {
	r.CandidateDriftCount.WithLabelValues(kind).Set(float64(count))
}
