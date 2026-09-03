"""Classification is deterministic integer arithmetic and nothing else."""

from __future__ import annotations

import datetime as dt

from reconcile.classify import (
    MODEL_RULES,
    classify_matched,
    classify_one_sided,
    explain,
    explain_whole_movement,
    find_timing_match,
)
from reconcile.model import Classification, Movement


def mv(txn_id: str, minor: int, day: int, account: str = "a1") -> Movement:
    d = dt.date(2028, 3, day)
    return Movement(txn_id, account, "USD", minor, d, d, "transfer")


def test_a_one_unit_difference_is_rounding() -> None:
    c, sig = classify_matched(48, 47)
    assert c is Classification.MODEL_DIFFERENCE
    assert sig == "round:1"


def test_basis_rules_tolerate_the_rounding_that_happened() -> None:
    # 5,000,000 minor at 325bp for 31 days: ACT/360 vs ACT/ACT(366).
    act360 = 5_000_000 * 31 * 325 // (10_000 * 360)
    actact = 5_000_000 * 31 * 325 // (10_000 * 366)
    c, sig = classify_matched(act360, actact)
    assert c is Classification.MODEL_DIFFERENCE
    assert sig == "basis:366_360", (act360, actact, sig)


def test_leap_basis_is_distinguishable_from_the_others() -> None:
    act365 = 5_000_000 * 31 * 325 // (10_000 * 365)
    actact = 5_000_000 * 31 * 325 // (10_000 * 366)
    c, sig = classify_matched(act365, actact)
    assert c is Classification.MODEL_DIFFERENCE
    assert sig == "basis:366_365"


def test_an_unexplained_delta_is_a_defect_not_a_guess() -> None:
    c, sig = classify_matched(1000, 17)
    assert c is Classification.DEFECT
    assert sig == "unexplained:983"


def test_a_fee_sized_delta_is_named_as_a_fee() -> None:
    c, sig = classify_matched(1200, 0, frozenset({1200, 500}))
    assert c is Classification.MODEL_DIFFERENCE
    assert sig == "fee:1200"


def test_rules_are_tried_in_a_fixed_order_so_signatures_are_stable() -> None:
    names = [r.name for r in MODEL_RULES]
    assert names[0] == "rounding", "the tightest rule must be tried first"
    assert len(set(names)) == len(names)
    for _ in range(5):
        assert classify_matched(48, 47)[1] == "round:1"


def test_timing_match_looks_for_the_same_money_on_another_day() -> None:
    other = [mv("t9", 5000, 3), mv("t8", 5000, 6)]
    got = find_timing_match(mv("t1", 5000, 2), other)
    assert got is not None and got.txn_id == "t9", "the nearest date wins"
    assert find_timing_match(mv("t1", 5000, 20), other) is None, "beyond the window"


def test_one_sided_with_a_nearby_counterpart_is_timing_not_a_defect() -> None:
    c, sig = classify_one_sided("legacy", mv("t1", 5000, 2), [mv("t9", 5000, 3)])
    assert c is Classification.TIMING and sig == "timing:+1d"


def test_one_sided_with_nothing_anywhere_is_a_defect() -> None:
    c, sig = classify_one_sided("legacy", mv("t1", 5000, 2), [])
    assert c is Classification.DEFECT and sig == "missing:shadow"


def test_a_delta_equal_to_one_whole_movement_is_named() -> None:
    assert explain_whole_movement(-7500, [mv("t1", 7500, 2)], "a1") == "txn:whole"
    assert explain_whole_movement(-7500, [mv("t1", 7500, 2)], "other") is None
    assert explain_whole_movement(0, [mv("t1", 7500, 2)], "a1") is None


def test_explain_returns_none_when_nothing_fits() -> None:
    assert explain(1000, 17) is None


def test_equal_amounts_are_never_a_model_difference() -> None:
    for rule in MODEL_RULES:
        assert not rule.predicate(500, 500), f"{rule.name} fired on identical amounts"
