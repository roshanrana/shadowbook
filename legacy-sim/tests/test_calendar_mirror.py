"""legacy_sim.calendar must agree with internal/bizdate, fact for fact.

The golden file is emitted by ``go run ./cmd/goldencal`` and covers every day of
2027-2029, every month boundary, the cut-off at four instants around 17:00, the
day-count bases and the two rounding modes. Risk R7 in the HLD is that the two
halves of LLD 5.4 drift; this test is the control for it.
"""

from __future__ import annotations

import datetime as dt
import json
from pathlib import Path

import pytest

from legacy_sim.calendar import (
    Basis,
    Calendar,
    basis_for,
    day_count_fraction,
    days_in_year,
    is_leap_year,
    round_half_even,
    round_half_up,
)

GOLDEN = Path(__file__).parent / "golden" / "calendar.json"


@pytest.fixture(scope="module")
def golden() -> dict[str, list[dict[str, object]]]:
    with GOLDEN.open(encoding="utf-8") as fh:
        data: dict[str, list[dict[str, object]]] = json.load(fh)
    return data


@pytest.fixture(scope="module")
def cal() -> Calendar:
    return Calendar()


def test_golden_file_is_substantial(golden: dict[str, list[dict[str, object]]]) -> None:
    # 2027-01-01 .. 2029-12-31 inclusive is 1096 days (2028 is a leap year).
    assert len(golden["days"]) == 1096
    assert len(golden["months"]) == 36
    assert len(golden["cutoffs"]) == 20


def test_business_days_match_go(cal: Calendar, golden: dict[str, list[dict[str, object]]]) -> None:
    mismatches: list[str] = []
    for fact in golden["days"]:
        d = dt.date.fromisoformat(str(fact["date"]))
        if cal.is_business_day(d) != fact["is_business_day"]:
            mismatches.append(f"{d}: python={cal.is_business_day(d)} go={fact['is_business_day']}")
        if cal.holiday_name(d) != (fact.get("holiday") or None):
            mismatches.append(f"{d}: holiday python={cal.holiday_name(d)!r} go={fact.get('holiday')!r}")
    assert not mismatches, "calendar drift between Go and Python:\n" + "\n".join(mismatches[:20])


def test_month_boundaries_match_go(cal: Calendar, golden: dict[str, list[dict[str, object]]]) -> None:
    for fact in golden["months"]:
        y, m = int(fact["year"]), int(fact["month"])  # type: ignore[arg-type]
        assert cal.first_of_month(y, m).isoformat() == fact["first_of_month"]
        assert cal.first_business_day_of_month(y, m).isoformat() == fact["first_business_day_of_month"]


def test_cutoff_matches_go(cal: Calendar, golden: dict[str, list[dict[str, object]]]) -> None:
    for fact in golden["cutoffs"]:
        instant = dt.datetime.fromisoformat(str(fact["instant"]).replace("Z", "+00:00")).replace(
            tzinfo=None
        )
        assert cal.business_date_for(instant).isoformat() == fact["business_date"], instant


def test_day_counts_match_go(golden: dict[str, list[dict[str, object]]]) -> None:
    for fact in golden["day_counts"]:
        num, den = day_count_fraction(
            dt.date.fromisoformat(str(fact["from"])),
            dt.date.fromisoformat(str(fact["to"])),
            Basis(str(fact["basis"])),
        )
        assert (num, den) == (fact["num"], fact["den"]), fact


def test_rounding_matches_go(golden: dict[str, list[dict[str, object]]]) -> None:
    for fact in golden["rounding"]:
        n, d = int(fact["num"]), int(fact["den"])  # type: ignore[arg-type]
        assert round_half_even(n, d) == fact["half_even"], fact
        assert round_half_up(n, d) == fact["half_up"], fact


# The D-010 window facts, asserted independently of the golden file so a
# regenerated golden cannot make them pass by agreeing with a broken Go side.


def test_d010_window_facts(cal: Calendar) -> None:
    w1 = cal.business_days_between(dt.date(2028, 2, 28), dt.date(2028, 4, 7))
    assert len(w1) == 30, "W1 must be exactly 30 business days"
    assert w1[0] == dt.date(2028, 2, 28) and w1[-1] == dt.date(2028, 4, 7)
    assert dt.date(2028, 2, 29) in w1, "W1 must contain the leap day for Q6"
    assert not cal.is_business_day(dt.date(2028, 4, 1)), "2028-04-01 is a Saturday; Q12 needs this"
    assert all(cal.holiday_name(d) is None for d in w1), "no holiday may fall inside W1"

    w2 = cal.business_days_between(dt.date(2028, 10, 2), dt.date(2028, 10, 13))
    assert len(w2) == 9, "W2 is 9 business days -- Columbus Day is excluded"
    assert dt.date(2028, 10, 9) not in w2
    assert cal.holiday_name(dt.date(2028, 10, 9)) == "Columbus Day"


def test_q12_needs_the_two_firsts_to_differ(cal: Calendar) -> None:
    assert cal.first_of_month(2028, 4) != cal.first_business_day_of_month(2028, 4)
    assert cal.first_of_month(2028, 3) == cal.first_business_day_of_month(2028, 3)


def test_leap_year_helpers() -> None:
    assert is_leap_year(2028) and days_in_year(2028) == 366
    assert not is_leap_year(2027) and days_in_year(2027) == 365
    assert not is_leap_year(1900) and is_leap_year(2000)
    assert basis_for(dt.date(2028, 6, 1)) is Basis.ACTACT
    assert basis_for(dt.date(2027, 6, 1)) is Basis.ACT365


def test_rounding_rejects_zero_denominator() -> None:
    for fn in (round_half_even, round_half_up):
        with pytest.raises(ZeroDivisionError):
            fn(1, 0)


def test_half_even_and_half_up_differ_only_on_ties() -> None:
    # If these agreed on ties, Q1 would be invisible to the reconciler.
    assert round_half_even(5, 2) == 2 and round_half_up(5, 2) == 3
    assert round_half_even(-5, 2) == -2 and round_half_up(-5, 2) == -3
    for n, d in ((1, 3), (2, 3), (11, 4), (9, 4), (-1, 3)):
        assert round_half_even(n, d) == round_half_up(n, d)


def test_next_and_prev_business_day(cal: Calendar) -> None:
    assert cal.next_business_day(dt.date(2028, 3, 31)) == dt.date(2028, 4, 3)
    assert cal.prev_business_day(dt.date(2028, 4, 3)) == dt.date(2028, 3, 31)
    assert cal.next_business_day(dt.date(2028, 10, 6)) == dt.date(2028, 10, 10)
