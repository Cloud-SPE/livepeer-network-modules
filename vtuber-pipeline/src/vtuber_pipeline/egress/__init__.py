"""Egress worker — chunked-POST receiver that pipes the request body
through ffmpeg `-c copy` to a destination RTMP URL.

Per [ADR-007](../../../../../docs/design-docs/decisions/007-egress-flow-pipeline-bearer.md),
the worker is a SERVER (the session-runner initiates the chunked POST).

See docs/exec-plans/active/mock-youtube-egress.md M2.
"""

__version__ = "0.0.0"
