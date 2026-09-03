"""Each quirk in isolation. Turning a quirk off must restore documented behaviour."""

from __future__ import annotations

import datetime as dt
from pathlib import Path

import pytest

from legacy_sim.calendar import Basis, Calendar, round_half_even
from legacy_sim.model import Txn
from legacy_sim.quirks import HOOKS, Quirks, load_specs

QUIRKS_YAML = Path(__file__).resolve().parents[1] / "quirks.yaml"


@pytest.fixture(scope="module")
def specs() -> dict[str, object]:
    return load_specs(QUIRKS_YAML)  # type: ignore[return-value]


def build(specs: dict, enabled: set[str]) -> Quirks:
    """A Quirks with exactly `enabled` switched on."""
    import copy

    s = copy.deepcopy(specs)
    for qid, spec in s.items():
        object.__setattr__(spec, "enabled", qid in enabled)
    return Quirks(s, Calendar())


def txn(amount: int, currency: str = "USD", at: dt.datetime | None = None, **kw) -> Txn:
    at = at or dt.datetime(2028, 3, 1, 10, 0, 0)
    return Txn(
        txn_id=kw.pop("txn_id", "t1"),
        account_id=kw.pop("account_id", "a1"),
        currency=currency,
        amount_minor=amount,
        posted_at=at,
        value_date=at.date(),
        business_date=at.date(),
        kind=kw.pop("kind", "transfer"),
        counterparty=kw.pop("counterparty", "CP-1"),
    )


def test_yaml_has_all_twelve_and_every_hook_is_implemented(specs: dict) -> None:
    assert sorted(specs) == sorted(f"Q{i}" for i in range(1, 13))
    assert sorted(HOOKS) == sorted(specs)
    for qid, spec in specs.items():
        assert spec.documented, f"{qid} has no documented behaviour"
        assert spec.legacy != spec.documented, f"{qid} does not actually diverge"


def test_all_quirks_off_means_documented_behaviour(specs: dict) -> None:
    """The control the whole measurement rests on."""
    q = build(specs, set())
    cal = Calendar()
    # Cut-off: exclusive at 17:00.
    assert q.business_date_for(dt.datetime(2028, 3, 1, 16, 59, 59, 999_000)) == dt.date(2028, 3, 1)
    assert q.business_date_for(dt.datetime(2028, 3, 1, 17, 0, 0)) == dt.date(2028, 3, 2)
    # Columbus Day is a holiday.
    assert not q.is_business_day(dt.date(2028, 10, 9))
    # Rounding is half-even.
    assert q.interest_rounding(5, 2) == round_half_even(5, 2) == 2
    # Basis follows the documented rule.
    assert q.interest_basis("SAV-01", dt.date(2028, 3, 1)) is Basis.ACTACT
    assert q.interest_basis("SAV-01", dt.date(2027, 3, 1)) is Basis.ACT365
    # Interest posts on the calendar first.
    assert q.interest_post_date(2028, 4) == cal.first_of_month(2028, 4) == dt.date(2028, 4, 1)
    # No fee waiver, fee basis is available balance, holds last 72 hours.
    assert q.monthly_fee(500, dt.date(2017, 1, 1)) == 500
    assert q.min_balance_basis(ledger_minor=999, available_minor=1) == 1
    placed = dt.datetime(2028, 3, 1, 10, 0, 0)
    assert q.hold_expiry(placed) == placed + dt.timedelta(hours=72)
    # Stream is untouched.
    ts = [txn(100), txn(100, txn_id="t2")]
    assert q.transform_transactions(ts) == ts


def test_q1_rounding_half_up(specs: dict) -> None:
    q = build(specs, {"Q1"})
    assert q.interest_rounding(5, 2) == 3  # documented half-even gives 2
    assert q.interest_rounding(7, 2) == 4  # a tie where the two agree
    assert q.interest_rounding(1, 3) == 0  # away from a tie they must agree


def test_q2_cutoff_fires_one_millisecond_early(specs: dict) -> None:
    q = build(specs, {"Q2"})
    edge = dt.datetime(2028, 3, 1, 16, 59, 59, 999_000)
    assert q.business_date_for(edge) == dt.date(2028, 3, 2)
    assert q.business_date_for(edge - dt.timedelta(milliseconds=1)) == dt.date(2028, 3, 1)


def test_q3_act360_only_on_the_named_product(specs: dict) -> None:
    q = build(specs, {"Q3"})
    assert q.interest_basis("SAV-01", dt.date(2028, 3, 1)) is Basis.ACT360
    assert q.interest_basis("CHK-01", dt.date(2028, 3, 1)) is Basis.ACTACT


def test_q4_waives_only_grandfathered_accounts(specs: dict) -> None:
    q = build(specs, {"Q4"})
    assert q.monthly_fee(500, dt.date(2017, 6, 1)) == 0
    assert q.monthly_fee(500, dt.date(2021, 6, 1)) == 500


def test_q5_makes_columbus_day_a_business_day(specs: dict) -> None:
    q = build(specs, {"Q5"})
    assert q.is_business_day(dt.date(2028, 10, 9))
    assert not q.is_business_day(dt.date(2028, 10, 7))  # a Saturday is still not
    assert not q.is_business_day(dt.date(2028, 12, 25))  # other holidays unaffected


def test_q6_stays_on_365_through_a_leap_year(specs: dict) -> None:
    q = build(specs, {"Q6"})
    assert q.interest_basis("CHK-01", dt.date(2028, 3, 1)) is Basis.ACT365
    assert q.interest_basis("CHK-01", dt.date(2027, 3, 1)) is Basis.ACT365


def test_q7_uses_ledger_balance_for_the_fee(specs: dict) -> None:
    q = build(specs, {"Q7"})
    # Funds are held, so available is low but ledger looks solvent.
    assert q.min_balance_basis(ledger_minor=200_000, available_minor=1_000) == 200_000


def test_q8_expires_at_midnight_on_plus_three(specs: dict) -> None:
    q = build(specs, {"Q8"})
    placed = dt.datetime(2028, 3, 1, 10, 0, 0)
    assert q.hold_expiry(placed) == dt.datetime(2028, 3, 4, 0, 0, 0)
    # Documented would be 2028-03-04 10:00, so the legacy hold dies 10h early.
    assert q.hold_expiry(placed) < placed + dt.timedelta(hours=72)


def test_q9_deletes_the_original_and_the_reversal(specs: dict) -> None:
    q = build(specs, {"Q9"})
    original = txn(1000, txn_id="orig")
    reversal = txn(-1000, txn_id="rev", kind="reversal", counterparty="orig")
    other = txn(50, txn_id="other")
    out = q.transform_transactions([original, reversal, other])
    assert [t.txn_id for t in out] == ["other"]
    # Invisible at the control-total grain, by design: the pair netted to zero.
    assert sum(t.amount_minor for t in [original, reversal]) == 0


def test_q10_truncates_jpy_and_leaves_usd_alone(specs: dict) -> None:
    q = build(specs, {"Q10"})
    import dataclasses

    usd = txn(1049, currency="USD", txn_id="u")
    # The core computed 1049.50 yen. Documented rounds half-even -> 1050;
    # Q10 truncates -> 1049. A one-yen shortfall, never an overshoot.
    jpy = dataclasses.replace(txn(1050, currency="JPY", txn_id="j"), true_hundredths=104_950)
    exact = dataclasses.replace(txn(1048, currency="JPY", txn_id="e"), true_hundredths=104_800)
    negative = dataclasses.replace(txn(-1050, currency="JPY", txn_id="n"), true_hundredths=-104_950)
    out = {t.txn_id: t.amount_minor for t in q.transform_transactions([usd, jpy, exact, negative])}
    assert out["u"] == 1049, "USD must be untouched"
    assert out["j"] == 1049, "1049.50 yen truncates to 1049, not 1050"
    assert out["e"] == 1048, "an exact amount loses nothing"
    assert out["n"] == -1049, "truncation is toward zero, so a debit shrinks too"


def test_q10_is_always_short_never_over(specs: dict) -> None:
    """Truncation can only lose value. If Q10 ever rounded up, the break sign
    would flip and the reconciler's rule would misattribute it."""
    import dataclasses

    q = build(specs, {"Q10"})
    from legacy_sim.calendar import round_half_even

    for base in range(100, 140):
        th = base * 100 + (50 if base % 2 else 0)
        documented = round_half_even(th, 100)
        t = dataclasses.replace(txn(documented, currency="JPY"), true_hundredths=th)
        legacy = q.transform_transactions([t])[0].amount_minor
        assert legacy <= documented, f"{th} hundredths: legacy {legacy} > documented {documented}"
        assert documented - legacy <= 1


def test_q11_suppresses_a_repeat_inside_the_window(specs: dict) -> None:
    q = build(specs, {"Q11"})
    base = dt.datetime(2028, 3, 1, 11, 0, 0)
    a = txn(7500, at=base, txn_id="a")
    b = txn(7500, at=base + dt.timedelta(seconds=20), txn_id="b")
    c = txn(7500, at=base + dt.timedelta(seconds=120), txn_id="c")
    out = [t.txn_id for t in q.transform_transactions([a, b, c])]
    assert out == ["a", "c"], "the 20s repeat is suppressed, the 120s one is not"


def test_q12_posts_on_the_first_business_day(specs: dict) -> None:
    q = build(specs, {"Q12"})
    # 2028-04-01 is a Saturday, so the legacy core posts on Monday the 3rd.
    assert q.interest_post_date(2028, 4) == dt.date(2028, 4, 3)
    # March 1 2028 is a Wednesday, so the two agree and Q12 cannot fire.
    assert q.interest_post_date(2028, 3) == dt.date(2028, 3, 1)


def test_enabled_ids_reflects_the_yaml(specs: dict) -> None:
    q = build(specs, {"Q1", "Q5"})
    assert q.enabled_ids() == {"Q1", "Q5"}
