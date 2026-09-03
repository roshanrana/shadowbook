"""D-010's windows, and the guard that stops the calendar defect returning."""

from __future__ import annotations

import datetime as dt

import pytest

from legacy_sim.calendar import Calendar
from legacy_sim.windows import (
    W1,
    W2,
    WINDOWS,
    UnreachableQuirkError,
    Window,
    assert_all_quirks_reachable,
    reachability,
)


def test_w1_is_exactly_thirty_business_days() -> None:
    cal = Calendar()
    days = W1.business_days(cal)
    assert len(days) == 30
    assert days[0] == dt.date(2028, 2, 28)
    assert days[-1] == dt.date(2028, 4, 7)


def test_w2_is_nine_business_days_because_columbus_day_is_excluded() -> None:
    cal = Calendar()
    days = W2.business_days(cal)
    assert len(days) == 9
    assert dt.date(2028, 10, 9) not in days


def test_every_quirk_is_reachable_in_at_least_one_window() -> None:
    reach = reachability(Calendar())
    unreachable = sorted(q for q, ws in reach.items() if not ws)
    assert not unreachable, f"{unreachable} cannot fire in any window; Finding 1 would under-report"


def test_the_two_windows_are_both_necessary() -> None:
    """Neither window alone covers all twelve. If one ever did, say so and drop
    the other -- but it cannot, which is why D-010 exists."""
    cal = Calendar()
    only_w1 = reachability(cal, (W1,))
    only_w2 = reachability(cal, (W2,))
    assert not only_w1["Q5"], "W1 must not reach Q5 (Columbus Day is in October)"
    assert not only_w2["Q6"], "W2 must not reach Q6 (the leap day is in February)"
    assert only_w1["Q6"] and only_w2["Q5"]


def test_guard_raises_for_an_unreachable_quirk() -> None:
    cal = Calendar()
    # A single two-day window in a quiet stretch reaches almost nothing.
    tiny = Window("TINY", dt.date(2028, 6, 6), dt.date(2028, 6, 7), "too short")
    with pytest.raises(UnreachableQuirkError) as exc:
        assert_all_quirks_reachable(cal, {"Q5", "Q6"}, (tiny,))
    assert "Q5" in str(exc.value) and "Q6" in str(exc.value)
    assert "D-010" in str(exc.value), "the error must point at the decision it protects"


def test_guard_passes_for_the_configured_windows() -> None:
    assert_all_quirks_reachable(Calendar(), {f"Q{i}" for i in range(1, 13)}, WINDOWS)
