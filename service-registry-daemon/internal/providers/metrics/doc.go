// Package metrics defines the Recorder provider — the only place in
// the repo permitted to import github.com/prometheus/client_golang.
// Service, repo, and other provider packages depend only on the
// Recorder interface; that's how the metrics surface stays swappable
// (Prometheus → OTLP → noop) without touching business logic.
//
// See docs/design-docs/observability.md for the full metric catalog,
// label value enums, and the per-orchestrator-via-audit-log
// philosophy.
package metrics
