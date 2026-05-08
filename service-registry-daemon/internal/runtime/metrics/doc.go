// Package metrics hosts the daemon's HTTP /metrics + /healthz
// listener. Distinct from internal/runtime/grpc (which serves
// unix-socket gRPC) because:
//
//  1. Prometheus expects pull over TCP HTTP; running it on the same
//     unix socket as the gRPC surface would force operators to run a
//     sidecar proxy.
//  2. The metrics surface has a different trust posture — it's
//     scrape-only, low-sensitivity, and operators want it on a
//     well-known port (9091 by default) for their existing scrapers.
//
// The listener is opt-in: it only runs when --metrics-listen is set.
// When unset, the daemon installs a Noop recorder and never opens
// a TCP socket.
package metrics
