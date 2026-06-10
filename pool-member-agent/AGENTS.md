# AGENTS.md

This is `pool-member-agent/` — the lightweight host-side process shipped in the
pool signup bundle.

## Scope

- Keep the member workflow simple: configure by environment, run under Docker
  Compose, make outbound-only calls.
- The agent must not run `capability-broker` or `payment-daemon`.
- Hardware inventory comes from the host runtime (`nvidia-smi` for NVIDIA GPUs)
  and is reported to `pool-controller` using the enrollment bearer token.
- Future tunnel/session code belongs here, but the broker remains the payment
  and routing authority.

## Build / Test

- `go test ./...`
- `go build ./cmd/pool-member-agent`
