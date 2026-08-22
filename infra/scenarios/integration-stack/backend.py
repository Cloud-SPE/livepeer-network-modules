"""Stub workload backend for the integration stack.

The inference is fake. The RESPONSE SHAPES ARE NOT — they follow the
OpenAI API precisely, because a downstream gateway parses them and a
convenient-but-wrong shape means it builds against fiction and breaks on
the real thing.

That includes the awkward parts:

  * streaming emits real `chat.completion.chunk` frames and terminates
    with `data: [DONE]`;
  * usage appears in a streamed response ONLY when the caller sets
    `stream_options.include_usage`, exactly as OpenAI behaves. If the
    gateway stops forcing that flag, a streamed job meters ZERO here —
    which is the point. Better to meet that in a pilot than in
    production;
  * token counts are derived from the actual request rather than being
    a constant, so a caller that meters gets numbers that move.

Ports: 9411 serves the OpenAI-shaped surface, 9500 the paid-session
runner. Two listeners rather than one mux, so a misconfigured backend
URL fails loudly instead of hiding behind a working one.
"""
import json
import re
import time
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer
from threading import Thread

SESSIONS = {}

SAMPLE = (
    "Probabilistic micropayments settle in expectation rather than per request, "
    "so the ledger only moves when a ticket wins."
)


def approx_tokens(text: str) -> int:
    """A rough token count. Deliberately not exact — nothing downstream
    should depend on a stub's tokenizer — but proportional to the input
    so metering sees numbers that vary."""
    return max(1, len(text) // 4)


def read_body(handler) -> bytes:
    """Read a request body under either framing.

    Content-Length is the easy case. CHUNKED is the one that matters:
    curl and most HTTP clients switch to it for file uploads, so a stub
    that only honoured Content-Length saw an empty body for exactly the
    multipart transcription requests it exists to serve — and answered
    as though every optional form field were absent.
    """
    if handler.headers.get("transfer-encoding", "").lower().strip() == "chunked":
        out = bytearray()
        while True:
            line = handler.rfile.readline().strip()
            if not line:
                continue
            try:
                size = int(line.split(b";")[0], 16)
            except ValueError:
                break
            if size == 0:
                handler.rfile.readline()  # trailing CRLF after the last chunk
                break
            out += handler.rfile.read(size)
            handler.rfile.read(2)  # CRLF after each chunk
        return bytes(out)
    n = int(handler.headers.get("content-length", 0) or 0)
    return handler.rfile.read(n) if n else b""


class OpenAIHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _json(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _sse(self, frames):
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.send_header("cache-control", "no-cache")
        self.send_header("connection", "keep-alive")
        self.send_header("transfer-encoding", "chunked")
        self.end_headers()
        for frame in frames:
            chunk = f"data: {json.dumps(frame)}\n\n".encode()
            self.wfile.write(b"%x\r\n%s\r\n" % (len(chunk), chunk))
            self.wfile.flush()
        done = b"data: [DONE]\n\n"
        self.wfile.write(b"%x\r\n%s\r\n" % (len(done), done))
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()

    # --- chat completions -------------------------------------------------

    def chat(self, req):
        model = req.get("model", "gpt-oss-20b")
        prompt = " ".join(
            m.get("content", "") for m in req.get("messages", []) if isinstance(m, dict)
        )
        prompt_tokens = approx_tokens(prompt) if prompt else 8
        completion = SAMPLE
        completion_tokens = approx_tokens(completion)
        created = int(time.time())
        cid = f"chatcmpl-{uuid.uuid4().hex[:24]}"

        if not req.get("stream"):
            return self._json(200, {
                "id": cid,
                "object": "chat.completion",
                "created": created,
                "model": model,
                "choices": [{
                    "index": 0,
                    "message": {"role": "assistant", "content": completion},
                    "logprobs": None,
                    "finish_reason": "stop",
                }],
                "usage": {
                    "prompt_tokens": prompt_tokens,
                    "completion_tokens": completion_tokens,
                    "total_tokens": prompt_tokens + completion_tokens,
                },
            })

        def chunk(delta=None, finish=None, usage=None):
            body = {
                "id": cid,
                "object": "chat.completion.chunk",
                "created": created,
                "model": model,
                "choices": [] if usage else [{
                    "index": 0,
                    "delta": delta or {},
                    "logprobs": None,
                    "finish_reason": finish,
                }],
            }
            if usage:
                body["usage"] = usage
            return body

        frames = [chunk(delta={"role": "assistant", "content": ""})]
        for word in completion.split(" "):
            frames.append(chunk(delta={"content": word + " "}))
        frames.append(chunk(delta={}, finish="stop"))

        # Only when asked, as OpenAI does. Without it a streamed exchange
        # carries no usage and an openai-usage extractor meters zero.
        opts = req.get("stream_options") or {}
        if opts.get("include_usage"):
            frames.append(chunk(usage={
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": prompt_tokens + completion_tokens,
            }))
        return self._sse(frames)

    # --- audio ------------------------------------------------------------

    def transcription(self, raw, content_type):
        # The broker meters this from the uploaded audio, not from
        # anything here, so the response carries no usage — matching
        # OpenAI, whose transcription responses do not either.
        fmt = "json"
        # Tolerate whatever headers a client puts between the disposition
        # line and the value — curl adds none, other clients add
        # Content-Type, and a regex that assumed one shape silently
        # ignored the field and always answered as if it were absent.
        m = re.search(
            rb'name="response_format"[^\r\n]*\r?\n(?:[^\r\n]+\r?\n)*\r?\n([^\r\n]+)',
            raw,
        )
        if m:
            fmt = m.group(1).decode(errors="replace").strip()
        text = "Probabilistic micropayments settle in expectation."
        if fmt == "verbose_json":
            return self._json(200, {
                "task": "transcribe",
                "language": "english",
                "duration": 3.0,
                "text": text,
                "segments": [{
                    "id": 0, "seek": 0, "start": 0.0, "end": 3.0, "text": text,
                    "tokens": [50364, 1770, 13], "temperature": 0.0,
                    "avg_logprob": -0.31, "compression_ratio": 1.24,
                    "no_speech_prob": 0.02,
                }],
            })
        if fmt in ("text", "srt", "vtt"):
            body = text.encode()
            self.send_response(200)
            self.send_header("content-type", "text/plain")
            self.send_header("content-length", str(len(body)))
            self.end_headers()
            return self.wfile.write(body)
        return self._json(200, {"text": text})

    # --- routing ----------------------------------------------------------

    def do_POST(self):
        raw = read_body(self)
        path = self.path.split("?")[0].rstrip("/")

        if "audio" in path or "transcription" in path:
            return self.transcription(raw, self.headers.get("content-type", ""))

        try:
            req = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            return self._json(400, {
                "error": {
                    "message": "We could not parse the JSON body of your request.",
                    "type": "invalid_request_error", "param": None, "code": None,
                }
            })
        return self.chat(req)

    def do_GET(self):
        if self.path.rstrip("/") == "/v1/models":
            return self._json(200, {"object": "list", "data": [
                {"id": "gpt-oss-20b", "object": "model", "created": 1700000000, "owned_by": "stub"},
                {"id": "whisper-1", "object": "model", "created": 1700000000, "owned_by": "stub"},
            ]})
        return self._json(200, {"ok": True})

    def log_message(self, *args):
        pass


class RunnerHandler(BaseHTTPRequestHandler):
    """paid-session runner: create, status, terminate.

    Returns a real `sfu-room/v1` descriptor shape. It does NOT emit usage
    events, so a session opened against it does not meter — that needs a
    real meeting runtime, and pretending otherwise would hide the gap.
    """
    protocol_version = "HTTP/1.1"

    def _json(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        read_body(self)
        if self.path.rstrip("/") != "/sessions":
            return self._json(404, {"error": "not found"})
        sid = f"rns_{uuid.uuid4().hex[:12]}"
        SESSIONS[sid] = {"state": "active"}
        return self._json(200, {
            "session_id": sid,
            "descriptor": {
                "schema": "sfu-room/v1",
                "public": {
                    "room_url": f"https://sfu.stub.invalid/rooms/{sid}",
                    "ice_servers": [{"urls": ["stun:stun.l.google.com:19302"]}],
                    "max_participants": 12,
                },
            },
            "callback_token": f"cb_{sid}",
        })

    def do_GET(self):
        m = re.match(r"^/sessions/([^/?]+)", self.path)
        if m:
            return self._json(200, {"state": SESSIONS.get(m.group(1), {}).get("state", "gone")})
        return self._json(200, {"ok": True})

    def do_DELETE(self):
        m = re.match(r"^/sessions/([^/?]+)", self.path)
        if m:
            SESSIONS.pop(m.group(1), None)
            return self._json(200, {"terminated": True})
        return self._json(404, {"error": "not found"})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    for port, handler in ((9411, OpenAIHandler), (9500, RunnerHandler)):
        srv = HTTPServer(("127.0.0.1", port), handler)
        Thread(target=srv.serve_forever, daemon=True).start()
    while True:
        time.sleep(3600)
