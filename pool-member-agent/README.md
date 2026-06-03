# pool-member-agent

Host-side agent for connected pool members.

The signup bundle sets:

- `POOL_CONTROLLER_URL`
- `POOL_BROKER_URL`
- `POOL_BROKER_QUIC_ADDR` when QUIC is enabled
- `POOL_ENROLLMENT_ID`
- `POOL_MEMBER_ETH_ADDRESS`
- `POOL_BROKER_SESSION_CREDENTIAL`
- `POOL_WORKER_BACKENDS` as `backend-id=http://local-runner,...`
- `POOL_ENROLLMENT_TOKEN_FILE`

The agent reports GPU inventory to:

`POST /member/v1/enrollments/{id}/hardware`

It then keeps one outbound worker session open to the broker. QUIC is preferred
when `POOL_BROKER_QUIC_ADDR` is present; WebSocket over `POOL_BROKER_URL` is the
fallback for networks that block UDP.

Only outbound connectivity is required. Pool members do not run a broker,
`payment-daemon`, TLS listener, or DNS entry.
