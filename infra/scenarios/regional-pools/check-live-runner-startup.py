#!/usr/bin/env python3
"""Local published CPU-build configuration probe; never a GPU readiness claim."""
import base64
import json
import os
import pathlib
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

IMAGE = "tztcloud/live-runner-cpu@sha256:a62bf490edb31ea5010bdac33dd728b4f0b348b3fcf47e9dfa97e80e3212ca92"


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def request(origin, path, token=None, body=None, method=None):
    headers = {"Authorization": "Bearer " + token} if token else {}
    if body is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(origin + path, headers=headers,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 method=method)
    try:
        with urllib.request.urlopen(req, timeout=2) as res:
            return res.status, res.read()
    except urllib.error.HTTPError as err:
        return err.code, err.read()


def main():
    # Require the already-published local artifact; this probe never pulls/builds.
    image_id = docker("image", "inspect", IMAGE, "--format", "{{.Id}}")
    name = "regional-live-startup-" + uuid.uuid4().hex[:12]
    volume = name + "-journal"
    try:
        docker("volume", "create", volume)
        with tempfile.TemporaryDirectory(prefix="regional-live-config-") as tmp:
            env = pathlib.Path(tmp) / "runtime.env"
            values = {key: base64.b64encode(secrets.token_bytes(32)).decode() for key in
                      ["LIVE_RUNNER_MASTER_KEY", "LIVE_RUNNER_BROKER_TOKEN", "LIVE_RUNNER_INTERNAL_MEDIA_TOKEN"]}
            values.update(LIVEPEER_PUBLIC_URL="https://member.invalid/r/local-probe",
                          LIVEPEER_PUBLIC_RTMP_URL="rtmps://member.invalid:1936",
                          LIVE_RUNNER_STATE_DIR="/var/lib/live-runner")
            fd = os.open(env, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as stream:
                stream.write("".join(key + "=" + value + "\n" for key, value in values.items()))
            docker("run", "-d", "--name", name, "--pull=never", "-p", "127.0.0.1::8080",
                   "--env-file", str(env), "-v", volume + ":/var/lib/live-runner", IMAGE)
            contracts = []
            session = {"session_id": "local-config-probe", "work_id": "local-probe-work",
                       "capability": "video:transcode.live", "offering": "gateway-ingest",
                       "session_params": {"schema": "rtmp-hls-session/v1", "publisher_mode": "gateway-relay",
                                          "output_profile": "live-standard", "metering_rendition": "720p",
                                          "storage": {"kind": "runner-local"}},
                       "callback_url": "http://127.0.0.1:9/events", "callback_token": "local-probe-callback"}
            descriptors = []
            for attempt in range(2):
                port = docker("port", name, "8080/tcp").split(":")[-1]
                origin = "http://127.0.0.1:" + port
                deadline = time.monotonic() + 30
                ready_status, ready_body = 0, b"no readiness response"
                while True:
                    try:
                        status, raw = request(origin, "/.well-known/livepeer-runner")
                        ready_status, ready_body = request(origin, "/ready")
                        if status == 200 and 200 <= ready_status < 300:
                            break
                    except (OSError, urllib.error.URLError):
                        pass
                    if time.monotonic() > deadline:
                        raise RuntimeError(f"CPU probe readiness: {ready_status} {ready_body.decode()}")
                    time.sleep(0.25)
                contract = json.loads(raw)
                assert contract["capability_id"] == "video:transcode.live", contract
                assert contract["protocol"] == "paid-session/v1", contract
                assert 200 <= request(origin, "/ready")[0] < 300
                assert request(origin, "/v1/sessions/missing")[0] == 401
                assert request(origin, "/v1/sessions/missing", values["LIVE_RUNNER_BROKER_TOKEN"])[0] == 404
                contracts.append(contract)
                code, response = request(origin, "/v1/sessions", values["LIVE_RUNNER_BROKER_TOKEN"], session)
                assert 200 <= code < 300, (code, response)
                descriptors.append(json.loads(response))
                if attempt == 0:
                    docker("restart", name)
            assert contracts[0] == contracts[1], "normal restart changed contract"
            assert descriptors[0] == descriptors[1], "restart replay changed session credentials or descriptor"
            runner_id = descriptors[0]["runner_session_id"]
            code, _ = request(origin, "/v1/sessions/" + runner_id, values["LIVE_RUNNER_BROKER_TOKEN"],
                              {"reason": "gateway_close"}, "DELETE")
            assert 200 <= code < 300, code
            code, raw = request(origin, "/v1/sessions/" + runner_id, values["LIVE_RUNNER_BROKER_TOKEN"])
            assert code == 200 and json.loads(raw)["state"] == "ended", (code, raw)
            print(json.dumps({"result": "pass", "image_id": image_id, "contract": contracts[0],
                              "checks": ["required startup config", "public discovery", "readiness", "private route bearer", "normal restart on persistent journal", "idempotent session replay after restart", "session termination"],
                              "limits": "CPU configuration probe only; no GPU encode, media publishing, callback delivery or funds movement"}, indent=2))
    finally:
        subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(["docker", "volume", "rm", volume], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
