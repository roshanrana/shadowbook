"""Deterministic break classification and the model-rule table (FR-R2, FR-R5).

No LLM, no heuristics, no learned thresholds. Every rule below is an integer
predicate a reader can check by hand, which is the point: a reconciliation
engine whose classifications cannot be reproduced by hand is not evidence.

Classification and quirk ATTRIBUTION are deliberately separate. Nothing in this
module knows which quirks are enabled, or that quirks exist at all. If it did,
the reconciler would be marking its own homework.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Callable, Sequence
from dataclasses import dataclass

from reconcile.model import Classification, Grain, Movement

# How far apart two sides may be and still count as a timing difference rather
# than a defect. Two business days is the ageing window of FR-R2.
TIMING_WINDOW_DAYS = 2


@dataclass(frozen=True, slots=True)
class ModelRule:
    """One explanation for a non-zero delta between matched records."""

    name: str
    signature: str
    predicate: Callable[[int, int], bool]
    description: str


def _rounding(legacy: int, shadow: int) -> bool:
    return abs(legacy - shadow) <= 1 and legacy != shadow


def _basis(legacy_den: int, shadow_den: int) -> Callable[[int, int], bool]:
    """Build a day-count-basis predicate.

    Both sides computed ``S x rate / (10000 x den)`` and rounded ONCE, so
    ``legacy x legacy_den`` and ``shadow x shadow_den`` both approximate the same
    unrounded quantity, each off by at most half a denominator. The test is
    therefore cross-multiplication with a tolerance of one denominator per side
    -- still exact integer arithmetic a reader can check by hand, and still
    deterministic, but not defeated by the rounding that necessarily happened.

    An earlier version demanded exact equality. It never matched once, because
    two independently rounded integers essentially never satisfy it.
    """

    def predicate(legacy: int, shadow: int) -> bool:
        if legacy == 0 or shadow == 0 or legacy == shadow:
            return False
        if (legacy > 0) != (shadow > 0):
            return False
        tolerance = legacy_den + shadow_den
        return abs(legacy * legacy_den - shadow * shadow_den) <= tolerance

    return predicate


def _scale_100(legacy: int, shadow: int) -> bool:
    """A scale-2 column read as whole units, or the reverse."""
    if legacy == 0 or shadow == 0:
        return False
    return abs(legacy) * 100 == abs(shadow) or abs(shadow) * 100 == abs(legacy)


# Order matters: the first rule that fits names the signature, so the tightest
# and least ambiguous rules come first.
MODEL_RULES: tuple[ModelRule, ...] = (
    ModelRule("rounding", "round:1", _rounding, "differs by at most one minor unit"),
    ModelRule("scale", "scale:100", _scale_100, "scale-2 storage read as whole units"),
    ModelRule(
        "basis_366_360", "basis:366_360", _basis(360, 366), "ACT/360 vs ACT/ACT in a leap year"
    ),
    ModelRule("basis_365_360", "basis:365_360", _basis(360, 365), "ACT/360 vs ACT/365"),
    ModelRule(
        "basis_366_365", "basis:366_365", _basis(365, 366), "ACT/365 vs ACT/ACT in a leap year"
    ),
)


def explain_fee(legacy_minor: int, shadow_minor: int, fee_amounts: frozenset[int]) -> str | None:
    """A delta equal to a configured fee amount is a fee that one side charged
    and the other did not."""
    delta = abs(legacy_minor - shadow_minor)
    return f"fee:{delta}" if delta in fee_amounts else None


def explain(legacy_minor: int, shadow_minor: int) -> ModelRule | None:
    """Return the first model rule that reproduces the delta, if any.

    Rules are tried in a fixed order, so the signature for a given pair of
    numbers is stable across runs (NFR-5).
    """
    for rule in MODEL_RULES:
        if rule.predicate(legacy_minor, shadow_minor):
            return rule
    return None


def classify_matched(
    legacy_minor: int, shadow_minor: int, fee_amounts: frozenset[int] = frozenset()
) -> tuple[Classification, str]:
    """Classify a delta between two records that DO match on key."""
    fee = explain_fee(legacy_minor, shadow_minor, fee_amounts)
    if fee is not None:
        return Classification.MODEL_DIFFERENCE, fee
    rule = explain(legacy_minor, shadow_minor)
    if rule is not None:
        return Classification.MODEL_DIFFERENCE, rule.signature
    return Classification.DEFECT, f"unexplained:{legacy_minor - shadow_minor}"


def explain_whole_movement(
    delta: int, other_side: Sequence[Movement], account_id: str | None
) -> str | None:
    """A delta equal to one whole movement on the other side.

    That is what a dropped or suppressed transaction looks like once it has been
    aggregated: the day's total is short by exactly one transaction. Q9 (a
    reversal deleting its original) and Q11 (duplicate suppression) both produce
    it, and neither is visible as a rounding or basis difference.
    """
    if delta == 0:
        return None
    for m in other_side:
        if account_id is not None and m.account_id != account_id:
            continue
        if abs(m.amount_minor) == abs(delta):
            return "txn:whole"
    return None


def find_timing_match(
    unmatched: Movement, other_side: Sequence[Movement], window_days: int = TIMING_WINDOW_DAYS
) -> Movement | None:
    """Look for the same money on the other side, a few business days away.

    Matching is on (account, currency, amount) -- deliberately NOT on txn_id,
    because a timing difference is the same money reported on a different date,
    and the ids usually still agree.
    """
    candidates = [
        m
        for m in other_side
        if m.account_id == unmatched.account_id
        and m.currency == unmatched.currency
        and m.amount_minor == unmatched.amount_minor
        and m.business_date != unmatched.business_date
        and abs((m.business_date - unmatched.business_date).days) <= window_days
    ]
    if not candidates:
        return None
    # Nearest date first, then id, so the choice is deterministic.
    return sorted(
        candidates, key=lambda m: (abs((m.business_date - unmatched.business_date).days), m.txn_id)
    )[0]


def classify_one_sided(
    present_on: str, movement: Movement, other_side: Sequence[Movement]
) -> tuple[Classification, str]:
    """Classify a record that appears on only one side."""
    match = find_timing_match(movement, other_side)
    if match is not None:
        offset = (match.business_date - movement.business_date).days
        return Classification.TIMING, f"timing:{offset:+d}d"
    return Classification.DEFECT, f"missing:{'shadow' if present_on == 'legacy' else 'legacy'}"


def grain_of(signature: str) -> Grain | None:
    """Only used for reporting; classification never depends on the grain."""
    return None


def business_days_between(a: dt.date, b: dt.date) -> int:
    return abs((b - a).days)
