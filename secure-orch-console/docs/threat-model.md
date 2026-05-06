# Threat model — secure-orch-console

Component-local, abbreviated. The full discussion lives in plan 0019
§3; this file is a quick reference for code reviewers and operators.

## Six attacker shapes

1. **Chain-RPC-only attacker.** Reads on-chain state, cannot alter
   manifest content. Defense: signature recovery must match the
   on-chain orch identity (mainnet only — Arbitrum One). Already the
   design.
2. **Broker-host FS access (warm-key compromise).** Attacker owns a
   broker's `host-config.yaml`, warm keystore, local TLS cert. Cannot
   sign a new manifest — cold key is on a different host. The
   console's diff renderer is the catch.
3. **secure-orch network reachability.** Should be impossible by the
   hard rule. Application contract: console + web UI bind `127.0.0.1`
   only (plan 0019 §6.1.1).
4. **Coordinator-host compromise (candidate poisoning).** The
   principal reason the sign cycle is operator-driven. The diff
   renderer surfaces extra capabilities, silent `price` /
   `worker_url` changes, eth_address swaps. Auto-sign is forbidden.
5. **Cold-key compromise.** Game over for this orch's identity until
   rotation. Defense in depth: V3 keystore (password-protected) or
   YubiHSM 2 (key never leaves the secure element). Rotation flow in
   plan 0019 §10.
6. **Operator coercion / regulatory action.** Out of architectural
   scope; physical control of the operator effectively *is* the orch.

## Console-local invariants

- The HTTP server binds `127.0.0.1` only. A startup test asserts the
  bound address.
- The `Signer` interface is the only path that emits a signature.
- Every console gesture (load_candidate, view_diff, sign,
  write_outbox, abort) emits a JSONL audit entry.
- `last-signed.json` is updated atomically (`rename(2)`) only after
  the outbox file is fully written.
