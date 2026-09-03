"""The documented calendar, day-count bases and rounding -- Python mirror.

This module is the Python half of LLD 5.4. ``internal/bizdate`` is the Go half.
They are golden-tested against each other over 2027-2029 (risk R7), so a change
to one that is not made to the other fails ``make check`` rather than quietly
producing a wrong Finding 1.

Nothing here reads the wall clock and nothing here uses ``float``. Interest
arithmetic is exact integer arithmetic, rounded once, at the end.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from enum import Enum

# The documented daily cut-off: 17:00:00.000, EXCLUSIVE. An instant strictly
# before it belongs to that day's business date; an instant at or after it rolls
# to the next business day. Q2 is the legacy core treating 16:59:59.999 as
# already belonging to the next day.
CUTOFF = dt.time(17, 0, 0, 0)


class Basis(str, Enum):
    """Day-count conventions.

    ACT365 and ACTACT are documented shadow behaviour (FR-L6). ACT360 is NOT --
    it exists only so the simulator and the reconciler's model-rule table can
    name what Q3 does.
    """

    ACT365 = "ACT/365"
    ACTACT = "ACT/ACT"
    ACT360 = "ACT/360"


@dataclass(frozen=True, slots=True)
class HolidayRule:
    name: str
    month: int
    day: int = 0  # fixed-date form; observed-shifted on a weekend fall
    nth: int = 0  # nth-weekday form; -1 means "last"
    weekday: int = 0  # Monday == 0, matching datetime.date.weekday()
    fixed: bool = False


# Columbus Day is a HOLIDAY in the documented calendar. Q5 is the legacy core
# treating it as a business day; removing this entry silently makes Q5
# undetectable.
FEDERAL_HOLIDAYS: tuple[HolidayRule, ...] = (
    HolidayRule("New Year's Day", 1, day=1, fixed=True),
    HolidayRule("Martin Luther King Jr. Day", 1, nth=3, weekday=0),
    HolidayRule("Washington's Birthday", 2, nth=3, weekday=0),
    HolidayRule("Memorial Day", 5, nth=-1, weekday=0),
    HolidayRule("Juneteenth", 6, day=19, fixed=True),
    HolidayRule("Independence Day", 7, day=4, fixed=True),
    HolidayRule("Labor Day", 9, nth=1, weekday=0),
    HolidayRule("Columbus Day", 10, nth=2, weekday=0),
    HolidayRule("Veterans Day", 11, day=11, fixed=True),
    HolidayRule("Thanksgiving", 11, nth=4, weekday=3),
    HolidayRule("Christmas Day", 12, day=25, fixed=True),
)


def is_leap_year(year: int) -> bool:
    return (year % 4 == 0 and year % 100 != 0) or year % 400 == 0


def days_in_year(year: int) -> int:
    return 366 if is_leap_year(year) else 365


def _days_in_month(year: int, month: int) -> int:
    if month == 12:
        return 31
    return (dt.date(year, month + 1, 1) - dt.timedelta(days=1)).day


def _nth_weekday(year: int, month: int, weekday: int, nth: int) -> dt.date:
    if nth == -1:
        d = dt.date(year, month, _days_in_month(year, month))
        while d.weekday() != weekday:
            d -= dt.timedelta(days=1)
        return d
    d = dt.date(year, month, 1)
    while d.weekday() != weekday:
        d += dt.timedelta(days=1)
    return d + dt.timedelta(days=7 * (nth - 1))


def _observed(d: dt.date) -> dt.date:
    """Saturday falls back to Friday; Sunday moves forward to Monday."""
    if d.weekday() == 5:
        return d - dt.timedelta(days=1)
    if d.weekday() == 6:
        return d + dt.timedelta(days=1)
    return d


class Calendar:
    """The documented US federal business-day calendar."""

    def __init__(self) -> None:
        self._cache: dict[int, dict[dt.date, str]] = {}

    def holidays(self, year: int) -> dict[dt.date, str]:
        cached = self._cache.get(year)
        if cached is not None:
            return cached
        out: dict[dt.date, str] = {}
        for r in FEDERAL_HOLIDAYS:
            d = (
                _observed(dt.date(year, r.month, r.day))
                if r.fixed
                else _nth_weekday(year, r.month, r.weekday, r.nth)
            )
            out[d] = r.name
        self._cache[year] = out
        return out

    def holiday_name(self, d: dt.date) -> str | None:
        return self.holidays(d.year).get(d)

    def is_business_day(self, d: dt.date) -> bool:
        if d.weekday() >= 5:
            return False
        return d not in self.holidays(d.year)

    def next_business_day(self, d: dt.date) -> dt.date:
        n = d + dt.timedelta(days=1)
        while not self.is_business_day(n):
            n += dt.timedelta(days=1)
        return n

    def prev_business_day(self, d: dt.date) -> dt.date:
        p = d - dt.timedelta(days=1)
        while not self.is_business_day(p):
            p -= dt.timedelta(days=1)
        return p

    def first_of_month(self, year: int, month: int) -> dt.date:
        """The CALENDAR first -- the documented interest posting date (FR-L6).

        Q12 posts on :meth:`first_business_day_of_month` instead. These two
        differing is exactly what makes Q12 detectable.
        """
        return dt.date(year, month, 1)

    def first_business_day_of_month(self, year: int, month: int) -> dt.date:
        d = dt.date(year, month, 1)
        return d if self.is_business_day(d) else self.next_business_day(d)

    def business_date_for(self, instant: dt.datetime) -> dt.date:
        """Map a wall-clock instant to its business date, cut-off EXCLUSIVE."""
        d = instant.date()
        if instant.time() >= CUTOFF:
            return self.next_business_day(d)
        if not self.is_business_day(d):
            return self.next_business_day(d)
        return d

    def business_days_between(self, start: dt.date, end: dt.date) -> list[dt.date]:
        """Every business day in [start, end], inclusive."""
        out: list[dt.date] = []
        d = start
        while d <= end:
            if self.is_business_day(d):
                out.append(d)
            d += dt.timedelta(days=1)
        return out


def basis_for(d: dt.date) -> Basis:
    """The documented basis: ACT/ACT in a leap year, ACT/365 otherwise (FR-L6)."""
    return Basis.ACTACT if is_leap_year(d.year) else Basis.ACT365


def day_count_fraction(start: dt.date, end: dt.date, basis: Basis) -> tuple[int, int]:
    """Return the accrual fraction as an exact ``(num, den)`` integer ratio.

    Never a float: the caller multiplies by principal and rate and rounds once,
    so no intermediate rounding can creep in.
    """
    num = (end - start).days
    if basis is Basis.ACT365:
        den = 365
    elif basis is Basis.ACTACT:
        den = days_in_year(start.year)
    else:
        den = 360
    return num, den


def round_half_even(num: int, den: int) -> int:
    """Round ``num/den`` to the nearest integer, ties to even.

    The documented rounding rule (FR-L6). Q1 is half-up, so a Q1 break is at
    most one minor unit -- which is why the reconciler's rounding rule tests
    ``abs(delta) <= 1``.
    """
    if den == 0:
        raise ZeroDivisionError("round_half_even: zero denominator")
    if den < 0:
        num, den = -num, -den
    neg = num < 0
    if neg:
        num = -num
    q, r = divmod(num, den)
    double = 2 * r
    if double > den or (double == den and q % 2 != 0):
        q += 1
    return -q if neg else q


def round_half_up(num: int, den: int) -> int:
    """Round half away from zero.

    NOT documented shadow behaviour -- this is what Q1 does, and it lives here
    so the simulator can name it. Never call it from a posting path.
    """
    if den == 0:
        raise ZeroDivisionError("round_half_up: zero denominator")
    if den < 0:
        num, den = -num, -den
    neg = num < 0
    if neg:
        num = -num
    q, r = divmod(num, den)
    if 2 * r >= den:
        q += 1
    return -q if neg else q
