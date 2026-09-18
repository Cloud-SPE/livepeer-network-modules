# member-portal

Shared wallet sign-in and presentation for independent regional pools. A login
never creates membership: each controller records its own explicit join and
versioned terms. The portal has no operator bearer credentials, payment keys,
or authority to edit regional books.

`make test` runs the race suite in Docker; `make build` builds the local image.
`make run` binds the HTTP backend to loopback for a separately configured HTTPS
proxy. Set the proxy's preserved Host to the configured public origin. Do not
expose the HTTP backend publicly. See [authentication](docs/authentication.md).

The portal API exposes `/api/auth/challenge`, `/api/auth/login`,
`/api/auth/logout`, `/api/session`, `/api/reports`, and an allowlisted regional
member API under `/api/regions/{pool_id}/`. Regional paths omit the
`/member/v1/` prefix there. No admin, agent desired-state, or agent stop-proof
route is proxied. One-use enrollment links are served at `/install/{secret}`
and `/bootstrap/{secret}/bundle`.

The server-rendered [member interface](docs/member-interface.md) covers regional
joins, one-command enrollment, device transfers, workload preferences and payout
history. See [qualified reporting](docs/reporting.md) for exact accounting and
freshness semantics. `make test-browser` exercises the portal in Chromium with
controlled regional fixtures and saves screenshots plus race/coverage evidence.
