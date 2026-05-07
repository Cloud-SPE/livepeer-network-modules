"""Tests for the per-session work-units reporter (gRPC bidi-stream surface)."""

from __future__ import annotations

import pytest

from session_runner.providers.work_units_reporter import WorkUnitsReporter


@pytest.mark.asyncio
async def test_initial_state_is_zero() -> None:
    rep = WorkUnitsReporter()
    assert rep.total == 0
    assert await rep.take_delta() == 0


@pytest.mark.asyncio
async def test_add_then_take_delta_returns_increment_and_resets_window() -> None:
    rep = WorkUnitsReporter()
    await rep.add(5)
    assert rep.total == 5
    assert await rep.take_delta() == 5
    assert await rep.take_delta() == 0


@pytest.mark.asyncio
async def test_monotonic_delta_accumulates_across_windows() -> None:
    rep = WorkUnitsReporter()
    await rep.add(3)
    await rep.add(4)
    assert await rep.take_delta() == 7
    await rep.add(1)
    assert await rep.take_delta() == 1
    assert rep.total == 8


@pytest.mark.asyncio
async def test_non_positive_units_are_ignored() -> None:
    rep = WorkUnitsReporter()
    await rep.add(0)
    await rep.add(-7)
    assert rep.total == 0
    assert await rep.take_delta() == 0
