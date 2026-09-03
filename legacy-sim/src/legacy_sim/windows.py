"""The demo windows of D-010, and the guard that keeps Finding 1 honest.

A 30-business-day window is about six calendar weeks and cannot contain both
Columbus Day (Q5, October) and a leap day (Q6, February) -- they are four and a
half months apart. Anchoring naively on the leap day also lands the month
boundary on Wednesday 1 March 2028, where the calendar first IS the first
business day, so Q12 could not fire either.

Two windows fix it. The reachability guard below is what stops the defect
returning: a quirk that no configured window can trigger would otherwise be
reported as "not detected" in Finding 1 for calendar reasons that say nothing
about the reconciliation.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass

from legacy_sim.calendar import Calendar


@dataclass(frozen=True, slots=True)
class Window:
    """A contiguous span of simulated business days."""

    window_id: str
    start: dt.date
    end: dt.date
    note: str

    def business_days(self, cal: Calendar) -> list[dt.date]:
        return cal.business_days_between(self.start, self.end)


# W1 is anchored at 2028-02-28 deliberately. Every claim is checked by a test:
# exactly 30 business days; contains the leap day 2028-02-29 (Q6); contains two
# month ends, 2028-02-29 and 2028-03-31 (Q4, Q7); 2028-04-01 is a SATURDAY, so
# the calendar first differs from the first business day (Q12); and the nearest
# federal holiday, Presidents Day 2028-02-21, falls outside.
W1 = Window(
    window_id="W1",
    start=dt.date(2028, 2, 28),
    end=dt.date(2028, 4, 7),
    note="leap day, two month ends, and a first-of-month that falls on a Saturday",
)

# W2 exists only for Q5. It spans 9 business days, not 10: Columbus Day is a
# holiday under the documented calendar and is therefore excluded from its own
# window, which is the point.
W2 = Window(
    window_id="W2",
    start=dt.date(2028, 10, 2),
    end=dt.date(2028, 10, 13),
    note="Columbus Day, Monday 2028-10-09",
)

WINDOWS: tuple[Window, ...] = (W1, W2)


class UnreachableQuirkError(AssertionError):
    """Raised when a quirk cannot fire in any configured window."""


def _has_leap_day(days: list[dt.date]) -> bool:
    return any(d.month == 2 and d.day == 29 for d in days)


def _has_month_end(days: list[dt.date], cal: Calendar) -> bool:
    return any((d + dt.timedelta(days=1)).month != d.month for d in days)


def _has_columbus_day(w: Window, cal: Calendar) -> bool:
    d = w.start
    while d <= w.end:
        if cal.holiday_name(d) == "Columbus Day":
            return True
        d += dt.timedelta(days=1)
    return False


def _has_divergent_first_of_month(days: list[dt.date], cal: Calendar) -> bool:
    """True when some month boundary in range has calendar-first != first business day."""
    seen: set[tuple[int, int]] = set()
    for d in days:
        for y, m in ((d.year, d.month),):
            if (y, m) in seen:
                continue
            seen.add((y, m))
            if cal.first_of_month(y, m) != cal.first_business_day_of_month(y, m):
                return True
    return False


def reachability(cal: Calendar, windows: tuple[Window, ...] = WINDOWS) -> dict[str, list[str]]:
    """Map each quirk id to the windows in which it can fire.

    A quirk with an empty list is unreachable and would be a silent false
    negative in Finding 1.
    """
    out: dict[str, list[str]] = {f"Q{i}": [] for i in range(1, 13)}
    for w in windows:
        days = w.business_days(cal)
        if not days:
            continue
        month_end = _has_month_end(days, cal)

        # Q1, Q2, Q9, Q10, Q11 are daily: any non-empty window triggers them.
        for q in ("Q1", "Q2", "Q9", "Q10", "Q11"):
            out[q].append(w.window_id)
        # Q3 and Q8 are daily too, but need an interest posting / a hold to
        # mature, so they need a window of at least a few days.
        if len(days) >= 3:
            out["Q3"].append(w.window_id)
            out["Q8"].append(w.window_id)
        # Q4 and Q7 are month-end fees.
        if month_end:
            out["Q4"].append(w.window_id)
            out["Q7"].append(w.window_id)
        # Q5 needs Columbus Day inside the span.
        if _has_columbus_day(w, cal):
            out["Q5"].append(w.window_id)
        # Q6 needs a leap day.
        if _has_leap_day(days):
            out["Q6"].append(w.window_id)
        # Q12 needs a month whose first is not a business day.
        if _has_divergent_first_of_month(days, cal):
            out["Q12"].append(w.window_id)
    return out


def assert_all_quirks_reachable(
    cal: Calendar, enabled: set[str], windows: tuple[Window, ...] = WINDOWS
) -> None:
    """Raise unless every enabled quirk can fire in at least one window.

    This runs before a demo renders. It is the control for HLD risk R1, and the
    reason T-039 depends on T-034 in the execution plan.
    """
    reach = reachability(cal, windows)
    unreachable = sorted(q for q in enabled if not reach.get(q))
    if unreachable:
        raise UnreachableQuirkError(
            "these quirks cannot fire in any configured window, so Finding 1 "
            f"would report them as undetected for calendar reasons: {unreachable}. "
            "See decisions.md D-010."
        )
