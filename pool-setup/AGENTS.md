# Pool setup

One-shot offline provisioning; no Docker socket, deployment, transactions,
manifest publication or funds movement. Use beads for work tracking.

Keep identity/material generation distinct from rendering. Imported material is
read-only. Reuse must never initialize an empty replacement controller. Preserve
keys and credentials on reruns; publish validated output as immutable revisions.
Use actual component parsers and existing key-generation binaries, not parallel
cryptographic implementations. Do not log passwords, private keys or RPC URLs.

Run Python tests and a built-container acceptance test with synthetic material.
Distinguish offline parser validation from wallet unlocking, ingress, chain,
hardware and live deployment evidence. This component does not implement failover,
DNS/ACME automation, recovery UI, or automatic treasury transfers.
