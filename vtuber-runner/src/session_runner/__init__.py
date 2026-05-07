"""Livepeer vtuber session-runtime workload binary.

Per-host session orchestrator. Each session spawns a headless Chromium
child loading the avatar-renderer dist; the runner drives the renderer
over a control-WS, encodes audio + video via PyAV, mux's them, and
trickle-publishes upstream. Per-second work-units are reported to the
broker over the `SessionRunnerControl.ReportWorkUnits` gRPC bidi-stream
(see plan 0012-followup §8 + plan 0013-vtuber OQ5 lock).
"""

__version__ = "0.1.0"
