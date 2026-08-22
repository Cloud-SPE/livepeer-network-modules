"""Stub workload backend for the integration stack.

Stands in for the runners that do the actual work, so downstream teams
can exercise the paid path — payment, metering, settlement, evidence —
without anybody having to run a model or an SFU first.

It is deliberately obvious about being a stub: the point is that the
money path is real, not that the inference is.
"""
import json
import re
from http.server import BaseHTTPRequestHandler, HTTPServer

SESSIONS = {}


class Handler(BaseHTTPRequestHandler):
    def _json(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        n = int(self.headers.get("content-length", 0))
        body = self.rfile.read(n)

        # paid-session runner: create
        if self.path.rstrip("/") == "/sessions":
            sid = f"rns_{len(SESSIONS) + 1}"
            SESSIONS[sid] = {"state": "active"}
            return self._json(200, {
                "session_id": sid,
                "descriptor": {
                    "schema": "sfu-room/v1",
                    "public": {
                        "room_url": f"https://stub.invalid/rooms/{sid}",
                        "ice_servers": [{"urls": "stun:stun.l.google.com:19302"}],
                    },
                },
                "callback_token": f"cb_{sid}",
            })

        # transcription: units come from the uploaded audio, not from here
        if "transcriptions" in self.path:
            return self._json(200, {"text": "stub transcription"})

        # chat completions: usage is what the extractor reads
        return self._json(200, {
            "choices": [{"text": "stub completion"}],
            "usage": {"total_tokens": 42},
        })

    def do_GET(self):
        m = re.match(r"^/sessions/(.+)$", self.path)
        if m:
            return self._json(200, {"state": SESSIONS.get(m.group(1), {}).get("state", "gone")})
        return self._json(200, {"ok": True})

    def do_DELETE(self):
        m = re.match(r"^/sessions/(.+)$", self.path)
        if m:
            SESSIONS.pop(m.group(1), None)
            return self._json(200, {"terminated": True})
        return self._json(404, {"error": "not found"})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    from threading import Thread
    # 9411 is the job backend, 9500 the session runner. Two ports because
    # the offerings point at different URLs and a single mux would hide
    # a misconfigured backend behind a working one.
    for port in (9411, 9500):
        Thread(target=HTTPServer(("127.0.0.1", port), Handler).serve_forever, daemon=True).start()
    import time
    while True:
        time.sleep(3600)
