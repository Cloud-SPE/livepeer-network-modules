"""LLM-output → renderer expression code.

Ported from `livepeer-vtuber-project/session-runner/src/session_runner/service/emotion_mapper.py`.
"""

from __future__ import annotations


_DEFAULT_EXPRESSION = "neutral"


def map_emotion(text: str) -> str:
    if not text:
        return _DEFAULT_EXPRESSION
    lowered = text.lower()
    if any(token in lowered for token in (":)", ":d", "haha", "lol")):
        return "happy"
    if any(token in lowered for token in (":(", "sad", "sorry")):
        return "sad"
    if any(token in lowered for token in ("!", "wow", "amazing")):
        return "surprised"
    return _DEFAULT_EXPRESSION
