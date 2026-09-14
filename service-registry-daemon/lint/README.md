# Custom lints and documentation generation

`make lint` runs golangci-lint, generated-document checks, doc-gardener and
no-unverified-manifest. `make doc-lint` runs the generated-document and current
document checks. Use `make docs-generate` to regenerate the manifest example.

## doc-gardener

The checker requires title, status and YYYY-MM-DD last-reviewed frontmatter on
design docs other than the index. Review dates older than 365 days fail.
Allowed statuses are proposed, accepted, verified and deprecated.

It checks relative file links in current Markdown across the component,
including examples and lint instructions. It skips HTTP(S)/mailto links and
does not validate anchors or Mermaid syntax. It cannot establish whether prose
matches code. Missing docs or filesystem traversal failures are errors.

Immutable `docs/references/` and `docs/exec-plans/completed/` trees retain their
original historical links and are excluded. Links from current docs **to**
those files must still resolve. Tests cover both the exclusion and detection of
broken current example/lint links.

## Manifest example

`tools/manifest-doc` reads the protocol module's minimal envelope fixture,
validates it with `types.DecodeCoordinatorEnvelope`, and renders
`docs/generated/manifest-example.md`. The fixture has a placeholder signature;
this check establishes shape, not cryptographic deployability. `--check` fails
when the generated document differs. Do not edit the output by hand.

## no-unverified-manifest

This is a deliberately limited source-text heuristic: outside tests and the
boundary decoder, it flags `json.Unmarshal` followed within 200 characters by
`Manifest`. It is not dataflow analysis and cannot prove a signature was
verified. Its diagnostic points to `types.DecodeCoordinatorEnvelope`; signature
recovery remains the resolver's responsibility.

## Other gates

`layer-check` is a stub; golangci-lint depguard enforces configured import
boundaries. `coverage-gate` is also a stub. `make coverage-check` runs coverage
and invokes it, but does not currently enforce the documented 75% floor.
Tracked in beads `lnm-gpd`. No current CI contract should claim otherwise.
