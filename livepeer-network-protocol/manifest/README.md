# manifest/

JSON Schema for the manifest format orchestrators publish at
`/.well-known/livepeer-registry.json`. Cross-cutting; any change here forces a
spec-wide SemVer bump.

**Status:** schema TBD per [plan 0002](../../docs/exec-plans/active/0002-define-interaction-modes-spec.md).

Will hold:

- `schema.json` — the canonical JSON Schema.
- `examples/` — concrete manifest examples for each mode.
- `changelog.md` — schema-change history.
