# verify/

The cross-cutting manifest-signature verifier. Resolvers, coordinators,
and gateways all consume signed manifests and re-run the same secp256k1
+ EIP-191 personal-sign recovery; this module is the single source of
truth so the bytes-identical guarantee with secure-orch-console's
signer holds.

Lives here (rather than under `secure-orch-console/`) because the
recovery path is shared by every receiver of a signed manifest. Only
secure-orch produces signatures; everyone else verifies them.

## Usage

```go
import (
    "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify"
    "github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/canonical"
)

// canonical bytes of the inner manifest payload
canonicalBytes, err := canonical.Bytes(manifest)
if err != nil { /* ... */ }

addr, err := verify.New().Recover(canonicalBytes, signatureBytes)
if err != nil { /* ErrSignatureMalformed or ErrSignatureMismatch */ }

// addr is the recovered signer; compare against the manifest's claimed
// orch.eth_address AND the on-chain ServiceRegistry entry for double-
// verify (plan 0019 §8).
```

## Source attribution

Ported with permission from the prior reference impl
`livepeer-modules-project/service-registry-daemon/internal/providers/verifier/verifier.go`
per plan 0019 §12 commit 3 + repo-root AGENTS.md lines 62–66. Local
adaptations: `EthAddress` is a local typed string (the prior impl
imported it from `internal/types/`); errors are sentinel + `%w`-
wrapped (the prior impl used a custom `ManifestValidationError`
struct).
