# Vendored OLV (Open-LLM-VTuber)

This directory is a **vendor lift** of the upstream
[Open-LLM-VTuber](https://github.com/Open-LLM-VTuber/Open-LLM-VTuber)
project. Per plan 0013-vtuber OQ2 lock, OLV is vendored here (not a git
submodule) — submodule complexity (init/update, recursive clone, CI
gotchas) isn't worth the friction for OLV's slow upstream release
cadence, and the rewrite's clean-slate philosophy prefers a single
source of truth in-tree.

## Upstream pin

- **Repository:** `https://github.com/Open-LLM-VTuber/Open-LLM-VTuber`
- **Commit:** `e7cc0ca8d0094139e8b7963826c38a5f9a14b067`
- **Last vendored:** 2026-05-07 (during plan 0013-vtuber Phase 2).
- **License:** MIT (see `./LICENSE`); Live2D submodule retains its own
  license (see `./LICENSE-Live2D.md`).

## Contents

The vendored tree is upstream OLV verbatim with the following local
exclusions applied during copy (none affect runtime semantics):

- `.git/`, `.github/` — upstream CI + git plumbing irrelevant in our
  monorepo.
- `__pycache__/`, `*.pyc` — Python bytecode caches.
- `node_modules/` — frontend submodule deps; rebuilt on demand if the
  frontend ever ships locally (currently unused; runner uses
  `vtuber-runner/avatar-renderer/` as its rendering surface).

The runner imports OLV via `session_runner.providers.olv_loader`, which
sys.path-injects `third_party/olv/src/` so OLV's modules resolve
without touching the venv layout.

## Rebase procedure

To pull a new upstream version:

```sh
# 1. Clone upstream alongside the rewrite (one-time).
git clone https://github.com/Open-LLM-VTuber/Open-LLM-VTuber /tmp/olv-upstream

# 2. Check out the desired commit.
cd /tmp/olv-upstream && git checkout <new-commit>

# 3. Re-sync into the vendor location, preserving local exclusions.
cd /path/to/livepeer-network-rewrite
rsync -a --delete \
    --exclude '.git' --exclude '.github' \
    --exclude '__pycache__' --exclude '*.pyc' \
    --exclude 'node_modules' \
    /tmp/olv-upstream/ vtuber-runner/third_party/olv/

# 4. Update the "Commit" + "Last vendored" lines in this UPSTREAM.md.

# 5. Commit:
#      `chore(vtuber-runner): rebase third_party/olv onto upstream <short-sha>`
```

Run `vtuber-runner` smoke tests before merging the rebase commit; OLV's
loader has occasional API drift that surfaces in
`session_runner.service.conversation`.

## Why no submodule

Per user-memory `feedback_submodule_url_protocol.md` + the rewrite's
clean-slate philosophy, submodules are avoided. Vendor lifts are
deliberate, atomic operations the doc-gardener can audit; the rebase
procedure above replaces what `git submodule update --remote` would do.
