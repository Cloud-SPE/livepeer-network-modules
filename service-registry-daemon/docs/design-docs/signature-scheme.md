---
title: Signature scheme
status: verified
last-reviewed: 2026-04-25
---

# Signature scheme

How the publisher signs a manifest and how the resolver verifies it.

## Why sign?

The on-chain `ServiceRegistry.serviceURI` is a *pointer* — anyone with the orchestrator's eth key controls it. The manifest itself lives off-chain at that pointer; without a signature, an attacker who could MITM the manifest URL could rewrite capabilities/prices freely.

Signing the manifest with the orchestrator's eth key (the same key the chain associates with their address) lets the resolver verify the manifest was authored by the same identity the chain trusts to set the pointer.

## Algorithm

`eth-personal-sign` over canonical bytes.

1. Compute the canonical bytes of the manifest (see below).
2. Apply Ethereum's `personal_sign` prefix:
   ```
   "\x19Ethereum Signed Message:\n" + len(canonical_bytes) + canonical_bytes
   ```
3. `keccak256` the prefixed bytes → 32-byte digest.
4. Sign the digest with the operator's secp256k1 key → 65-byte `r || s || v` (v in {27, 28} or {0, 1}; we normalize to {27, 28} on emit).

`signature.alg` MUST be the exact string `"eth-personal-sign"` in v1. This leaves room for future EIP-712 typed-data support without breaking existing manifests.

## Canonical bytes procedure

Goal: a deterministic byte representation of "the manifest minus the signature" so signing and verifying produce the same input.

1. Take the manifest object.
2. Set `signature` to `null` (preserving the field key — do not delete).
3. Walk the object recursively. For every object:
   - Sort keys lexicographically (codepoint order).
   - Emit no whitespace between tokens.
4. Numbers are emitted in their JSON canonical form (no leading zeros, no trailing zeros for floats, no exponent form unless required).
5. Strings are JSON-encoded (escapes per RFC 8259).
6. UTF-8 byte representation is the result.

The `signed_canonical_bytes_sha256` field in the manifest is the SHA-256 of these bytes. It's not used for verification — verification recomputes the bytes — but it's a useful diagnostic field that lets operators eyeball whether two manifests were canonicalized identically.

## Verification

The resolver:

1. Receives the manifest bytes from the fetcher.
2. JSON-decodes into the Go struct.
3. Re-canonicalizes (the same algorithm).
4. Computes `keccak256(personal_sign_prefix + canonical_bytes)`.
5. Calls `crypto.SigToPub(digest, signature_bytes)` to recover the public key.
6. Derives the eth address from the public key.
7. Compares (case-insensitive) to:
   - `manifest.eth_address` (must match), AND
   - The eth address whose `serviceURI` pointed us here (must match).

A mismatch on either is a `signature_mismatch` error. The resolver does NOT cache a manifest with a mismatched signature.

## Why not EIP-712?

EIP-712 typed-data signing would be more elegant (visible field-by-field in MetaMask-style UIs) but adds a domain-separator concept and a much larger spec surface. For v1 we want the simplest thing that works with off-the-shelf go-ethereum tooling. EIP-712 is a candidate v2 alg under the same `signature.alg` extensibility.

## Key custody

Default: the publisher reads a V3 JSON keystore from `--keystore-path`, decrypts with `--keystore-password-file` or `LIVEPEER_KEYSTORE_PASSWORD` env. The decrypted key sits in process memory until shutdown.

For higher-assurance setups, the `Signer` provider interface accepts any implementation. HSM / KMS impls are listed in `tech-debt-tracker.md` as a planned v2 swap.

## Hot vs cold orch identity

If the operator's orchestrator identity is a cold wallet, they may not want to load it into the publisher daemon at all. The publisher supports a hot/cold split:

- The cold key is used **once** to sign a long-lived `delegation` document (lives in the manifest under `extra.delegation`) authorizing a hot key.
- The hot key signs the manifest.
- The resolver, if the manifest contains a delegation, verifies the chain matches: cold-key-signed-delegation → hot-key-signed-manifest → claimed `eth_address`.

This is a v1.1 feature flagged behind `--allow-delegated-signing`. See `tech-debt-tracker.md` (`hot-cold-delegation`).
