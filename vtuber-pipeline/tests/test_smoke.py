"""Smoke tests that keep `make test` green before real code lands.

Remove these once Pipeline has behavior to test.
"""

import vtuber_pipeline


def test_package_version_exposed() -> None:
    assert isinstance(vtuber_pipeline.__version__, str)
    assert vtuber_pipeline.__version__ != ""
