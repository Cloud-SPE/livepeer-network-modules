"""Tests for the LLM-output → renderer expression mapper."""

from __future__ import annotations

import pytest

from session_runner.service.emotion_mapper import map_emotion


@pytest.mark.parametrize(
    ("text", "expected"),
    [
        ("haha that's great :)", "happy"),
        ("LOL :D", "happy"),
        ("oh no :(", "sad"),
        ("sorry about that", "sad"),
        ("Wow! amazing!", "surprised"),
        ("hello there", "neutral"),
        ("", "neutral"),
    ],
)
def test_map_emotion_routes_obvious_cues(text: str, expected: str) -> None:
    assert map_emotion(text) == expected
