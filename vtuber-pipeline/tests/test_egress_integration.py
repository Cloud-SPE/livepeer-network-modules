"""End-to-end integration tests for the egress preview chain.

Requires the egress compose at `infrastructure/egress/docker-compose.yml`
to be running (`make egress-up`). These tests:

  - drive the mock-youtube + egress + nginx-rtmp stack with real HTTP,
  - push real MP2T bytes from a local ffmpeg testsrc,
  - assert the HLS manifest at the nginx-rtmp sink populates within 10s,
  - run `ffprobe` against the manifest to confirm the stream is decodable.

Marked `integration`; opt-in via `pytest -m integration`. The default
unit-test path (`make test-python`) skips them so contributors don't
need ffmpeg + docker on PATH.
"""

from __future__ import annotations

import json
import shutil
import subprocess
import time
import urllib.error
import urllib.request

import pytest

pytestmark = pytest.mark.integration


MOCK_YOUTUBE_URL = "http://localhost:8090"
EGRESS_URL = "http://localhost:8091"
HLS_URL = "http://localhost:8085"


def _is_up(url: str, timeout: float = 1.0) -> bool:
    try:
        with urllib.request.urlopen(f"{url}/api/health", timeout=timeout) as r:
            return r.status == 200
    except (TimeoutError, urllib.error.URLError, urllib.error.HTTPError):
        return False


@pytest.fixture(scope="module", autouse=True)
def require_stack() -> None:
    if shutil.which("ffmpeg") is None:
        pytest.skip("ffmpeg not on PATH — install or run `make egress-up`")
    if not _is_up(MOCK_YOUTUBE_URL):
        pytest.skip("mock-youtube not reachable — run `make egress-up` first")
    if not _is_up(EGRESS_URL):
        pytest.skip("egress not reachable — run `make egress-up` first")
    if not _is_up(HLS_URL):
        pytest.skip("nginx-rtmp not reachable — run `make egress-up` first")


def _register(session_id: str) -> tuple[str, str]:
    body = json.dumps(
        {"rtmp_url": "rtmp://nginx-rtmp:1935/live", "stream_key": session_id}
    ).encode()
    req = urllib.request.Request(
        f"{EGRESS_URL}/admin/sessions/{session_id}/register",
        data=body,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as r:
        reg = json.loads(r.read())
    bearer = reg["auth"].split(" ", 1)[1]
    egress_url = reg["egress_url"].replace("//egress:", "//localhost:")
    return bearer, egress_url


def _push_testsrc(egress_url: str, bearer: str, *, duration: int = 5) -> int:
    """Spawn ffmpeg → chunked POST. Returns the HTTP status."""
    ff = subprocess.Popen(
        [
            "ffmpeg",
            "-hide_banner",
            "-loglevel",
            "error",
            "-re",
            "-f",
            "lavfi",
            "-i",
            f"testsrc=size=320x240:rate=15:duration={duration}",
            "-f",
            "lavfi",
            "-i",
            f"sine=frequency=440:duration={duration}",
            "-c:v",
            "libx264",
            "-preset",
            "ultrafast",
            "-tune",
            "zerolatency",
            "-b:v",
            "500k",
            "-c:a",
            "aac",
            "-b:a",
            "64k",
            "-f",
            "mpegts",
            "pipe:1",
        ],
        stdout=subprocess.PIPE,
    )
    assert ff.stdout is not None

    def chunks():
        try:
            while True:
                buf = ff.stdout.read(64 * 1024)
                if not buf:
                    break
                yield buf
        finally:
            ff.stdout.close()

    req = urllib.request.Request(
        egress_url,
        data=chunks(),
        headers={"Authorization": f"Bearer {bearer}", "Transfer-Encoding": "chunked"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=duration + 30) as r:
            status = r.status
    finally:
        ff.wait(timeout=10)
    return status


def _wait_for_hls(session_id: str, timeout_secs: float = 15.0) -> str:
    """Poll the HLS manifest URL until #EXTM3U appears or we timeout."""
    deadline = time.time() + timeout_secs
    last_err: str | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"{HLS_URL}/hls/{session_id}.m3u8", timeout=2) as r:
                body = r.read().decode()
            if "#EXTM3U" in body:
                return body
        except urllib.error.HTTPError as e:
            last_err = f"HTTP {e.code}"
        except urllib.error.URLError as e:
            last_err = str(e)
        time.sleep(0.5)
    raise AssertionError(f"HLS manifest never populated (last: {last_err})")


# ── tests ─────────────────────────────────────────────────────────────


def test_full_pipeline_produces_hls() -> None:
    """The capstone integration: testsrc → chunked POST → ffmpeg →
    RTMP → nginx-rtmp → HLS playlist. Within 15s of pushing 5s of
    media, the manifest must contain at least one segment and
    ffprobe must decode it as h264 + aac."""
    session_id = f"int-{int(time.time())}"
    bearer, egress_url = _register(session_id)

    status = _push_testsrc(egress_url, bearer, duration=5)
    assert status == 200

    manifest = _wait_for_hls(session_id, timeout_secs=15)
    # At least one segment line.
    assert "#EXTINF" in manifest
    assert ".ts" in manifest

    # ffprobe the manifest. We assert against codec names, not exact
    # stream counts (the rtmp-module may interleave PMT etc.).
    out = subprocess.check_output(
        [
            "ffprobe",
            "-hide_banner",
            "-v",
            "error",
            "-show_entries",
            "stream=codec_name",
            f"{HLS_URL}/hls/{session_id}.m3u8",
        ],
        timeout=10,
    ).decode()
    assert "h264" in out
    assert "aac" in out


def test_mock_youtube_full_lifecycle() -> None:
    """Drive mock-youtube's four endpoints against the running container."""
    # liveBroadcasts.insert
    req = urllib.request.Request(
        f"{MOCK_YOUTUBE_URL}/youtube/v3/liveBroadcasts",
        data=json.dumps(
            {"snippet": {"title": "int-test"}, "status": {"privacyStatus": "unlisted"}}
        ).encode(),
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as r:
        bcast = json.loads(r.read())
    bid = bcast["id"]
    assert bcast["status"]["lifeCycleStatus"] == "created"

    # liveStreams.insert
    req = urllib.request.Request(
        f"{MOCK_YOUTUBE_URL}/youtube/v3/liveStreams",
        data=b"{}",
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as r:
        stream = json.loads(r.read())
    sid = stream["id"]

    # bind
    req = urllib.request.Request(
        f"{MOCK_YOUTUBE_URL}/youtube/v3/liveBroadcasts/bind?id={bid}&streamId={sid}",
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as r:
        bound = json.loads(r.read())
    assert bound["contentDetails"]["boundStreamId"] == sid
    assert bound["status"]["lifeCycleStatus"] == "ready"

    # transition through the happy path
    for target in ("testing", "live", "complete"):
        req = urllib.request.Request(
            f"{MOCK_YOUTUBE_URL}/youtube/v3/liveBroadcasts/transition?id={bid}&broadcastStatus={target}",
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=5) as r:
            updated = json.loads(r.read())
        assert updated["status"]["lifeCycleStatus"] == target


def test_egress_admin_minted_bearer_works_against_running_egress() -> None:
    """The bearer minted by /admin/sessions/<sid>/register must validate
    against the same /egress/sessions/<sid>/media endpoint. End-to-end
    check that the secret in EGRESS_BEARER_SECRET matches between
    minter and validator inside the container."""
    session_id = f"bearer-{int(time.time())}"
    bearer, egress_url = _register(session_id)
    # Push a tiny stub. We expect 200 (the ffmpeg -c copy may fail to
    # parse the bytes — that's fine; we're testing auth here, not the
    # full media path).
    req = urllib.request.Request(
        egress_url,
        data=b"\x47" * 1024,  # MP2T sync byte * 1KB
        headers={"Authorization": f"Bearer {bearer}"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            status = r.status
    except urllib.error.HTTPError as e:
        # ffmpeg may return 500 because the bytes aren't valid MP2T.
        # That's an INFO-level failure for this test; we only care
        # that auth passed (i.e., we did NOT get 401/404/409).
        status = e.code
    assert status not in (401, 404, 409), f"unexpected status {status}"
