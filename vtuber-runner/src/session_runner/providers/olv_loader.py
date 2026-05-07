"""OLV upstream loader — imports from `vtuber-runner/third_party/olv/`.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/providers/olv_loader.py`.
The vendored upstream is sys.path-injected at runtime so OLV's modules resolve
without touching the venv layout. See `third_party/olv/UPSTREAM.md` for the
upstream commit hash + rebase procedure.
"""

from __future__ import annotations

import sys
from pathlib import Path


def ensure_olv_on_path() -> Path:
    here = Path(__file__).resolve()
    olv_root = here.parents[3] / "third_party" / "olv"
    if not olv_root.is_dir():
        raise FileNotFoundError(
            f"vendored OLV not found at {olv_root}; run `vtuber-runner/third_party/olv/`"
            " sync per UPSTREAM.md"
        )
    olv_src = olv_root / "src"
    if str(olv_src) not in sys.path:
        sys.path.insert(0, str(olv_src))
    return olv_root
