"""The twelve seeded quirks, one pure function each.

``quirks.yaml`` is the source of truth for what each quirk is and whether it is
enabled; this module implements them. Every quirk is a pure function at one
named hook, which is what makes "quirk off" a genuine control and lets a test
run each one in isolation.

The ``documented`` behaviour of each quirk is what the shadow ledger does, and
lives in :mod:`legacy_sim.calendar` and the Go ``internal/bizdate``. Nothing in
this module is documented behaviour -- it is all divergence, on purpose.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

from legacy_sim.calendar import (
    Basis,
    Calendar,
    basis_for,
    is_leap_year,
    round_half_even,
    round_half_up,
)
from legacy_sim.model import Txn


@dataclass(frozen=True, slots=True)
class QuirkSpec:
    """One row of quirks.yaml."""

    quirk_id: str
    name: str
    enabled: bool
    legacy: str
    documented: str
    cadence: str
    expected_grain: str
    params: dict[str, Any] = field(default_factory=dict)


def load_specs(path: Path) -> dict[str, QuirkSpec]:
    """Read quirks.yaml. Keyed by id, so iteration order is deterministic."""
    with path.open(encoding="utf-8") as fh:
        raw = yaml.safe_load(fh)
    out: dict[str, QuirkSpec] = {}
    for row in raw["quirks"]:
        out[row["id"]] = QuirkSpec(
            quirk_id=row["id"],
            name=row["name"],
            enabled=bool(row["enabled"]),
            legacy=row["legacy"],
            documented=row["documented"],
            cadence=row["cadence"],
            expected_grain=row["expected_grain"],
            params=dict(row.get("params") or {}),
        )
    missing = [f"Q{i}" for i in range(1, 13) if f"Q{i}" not in out]
    if missing:
        raise ValueError(f"quirks.yaml is missing {missing}")
    return out


class Quirks:
    """The active quirk set, exposing one method per hook.

    Each method returns DOCUMENTED behaviour when its quirk is disabled, so
    turning every quirk off makes the simulator agree with the shadow exactly --
    that equality is the control the whole measurement rests on.
    """

    def __init__(self, specs: dict[str, QuirkSpec], cal: Calendar) -> None:
        self.specs = specs
        self.cal = cal

    def on(self, quirk_id: str) -> bool:
        spec = self.specs.get(quirk_id)
        return spec is not None and spec.enabled

    def enabled_ids(self) -> set[str]:
        return {q for q, s in self.specs.items() if s.enabled}

    # -- Q2, Q5: which business date an instant belongs to --------------------

    def is_business_day(self, d: dt.date) -> bool:
        """Q5: Columbus Day is a business day to the legacy core."""
        if self.on("Q5") and self.cal.holiday_name(d) == "Columbus Day":
            return d.weekday() < 5
        return self.cal.is_business_day(d)

    def business_date_for(self, instant: dt.datetime) -> dt.date:
        """Q2: the legacy cut-off fires at 16:59:59.999, one millisecond early.

        Documented is 17:00:00.000 exclusive, so a transaction stamped
        16:59:59.999 belongs to today on the shadow and to tomorrow on the
        legacy core. That single millisecond is the whole of Q2.
        """
        d = instant.date()
        cutoff = dt.time(16, 59, 59, 999_000) if self.on("Q2") else dt.time(17, 0, 0, 0)
        rolled = instant.time() >= cutoff

        if not rolled and self.is_business_day(d):
            return d
        n = d + dt.timedelta(days=1) if rolled else d
        while not self.is_business_day(n):
            n += dt.timedelta(days=1)
        return n

    # -- Q9, Q10, Q11: the transaction stream ---------------------------------

    def transform_transactions(self, txns: list[Txn]) -> list[Txn]:
        """Apply every stream-level quirk in a fixed order, so runs repeat."""
        out = list(txns)
        if self.on("Q10"):
            out = self._q10_jpy_truncated(out)
        if self.on("Q11"):
            out = self._q11_duplicate_suppression(out)
        if self.on("Q9"):
            out = self._q9_reversal_deletes_original(out)
        return out

    @staticmethod
    def _q10_jpy_truncated(txns: list[Txn]) -> list[Txn]:
        """JPY held in a scale-2 column and truncated back to whole yen.

        The core computes a value in hundredths of a yen (``true_hundredths``).
        The documented ledger rounds that half-even to whole yen -- JPY is scale
        0. The legacy core truncates toward zero instead, so it is always short
        by up to one yen, and never over.

        A JPY amount that is already exact loses nothing, which is why this
        quirk fires on some transactions and not others -- realistic, and it
        gives the reconciler a partial signal to age rather than a wall of
        identical breaks.
        """
        out: list[Txn] = []
        for t in txns:
            if t.currency != "JPY" or t.true_hundredths is None:
                out.append(t)
                continue
            th = t.true_hundredths
            # Truncation toward zero, which is what a scale-2 column does when
            # the extract writer casts it to an integer yen.
            truncated = -((-th) // 100) if th < 0 else th // 100
            out.append(t.with_amount(truncated))
        return out

    def _q11_duplicate_suppression(self, txns: list[Txn]) -> list[Txn]:
        """Same amount and counterparty within 60 seconds is silently dropped.

        The shadow has no suppression -- duplicates are the client's problem --
        so a legitimate repeat payment vanishes from the legacy side only.
        """
        window = int(self.specs["Q11"].params.get("window_seconds", 60))
        kept: list[Txn] = []
        for t in sorted(txns, key=lambda x: (x.posted_at, x.txn_id)):
            dup = any(
                k.account_id == t.account_id
                and k.amount_minor == t.amount_minor
                and k.counterparty == t.counterparty
                and 0 <= (t.posted_at - k.posted_at).total_seconds() <= window
                for k in kept
            )
            if not dup:
                kept.append(t)
        return kept

    @staticmethod
    def _q9_reversal_deletes_original(txns: list[Txn]) -> list[Txn]:
        """A reversal deletes the original instead of posting a contra entry.

        Invisible at the control-total grain by design (quirks.yaml says so):
        dropping both the original and its reversal leaves the day's total
        unchanged, so only the transaction grain can see it.
        """
        reversed_ids = {t.counterparty for t in txns if t.kind == "reversal" and t.counterparty}
        if not reversed_ids:
            return txns
        return [t for t in txns if t.txn_id not in reversed_ids and t.kind != "reversal"]

    # -- Q1, Q3, Q6: interest -------------------------------------------------

    def interest_basis(self, product_code: str, on: dt.date) -> Basis:
        """Q3: ACT/360 on one product. Q6: ACT/365 through a leap year."""
        if self.on("Q3") and product_code == self.specs["Q3"].params.get("product_code"):
            return Basis.ACT360
        if self.on("Q6") and is_leap_year(on.year):
            return Basis.ACT365
        return basis_for(on)

    def interest_rounding(self, num: int, den: int) -> int:
        """Q1: round half-up instead of half-even.

        The two agree except on exact ties, which is why a Q1 break is at most
        one minor unit -- and why the reconciler's rounding rule tests for that.
        """
        return round_half_up(num, den) if self.on("Q1") else round_half_even(num, den)

    def interest_post_date(self, year: int, month: int) -> dt.date:
        """Q12: post on the first BUSINESS day rather than the calendar first."""
        if self.on("Q12"):
            return self.cal.first_business_day_of_month(year, month)
        return self.cal.first_of_month(year, month)

    # -- Q4, Q7: fees ---------------------------------------------------------

    def monthly_fee(self, fee_minor: int, opened_on: dt.date) -> int:
        """Q4: waived for accounts opened before a grandfather date."""
        if not self.on("Q4"):
            return fee_minor
        raw = self.specs["Q4"].params.get("opened_before", "2019-01-01")
        return 0 if opened_on < dt.date.fromisoformat(str(raw)) else fee_minor

    def min_balance_basis(self, ledger_minor: int, available_minor: int) -> int:
        """Q7: assess the minimum-balance fee on the LEDGER balance.

        Documented behaviour is the available balance, so an account whose funds
        are held still looks solvent to the legacy core and escapes the fee.
        """
        return ledger_minor if self.on("Q7") else available_minor

    # -- Q8: holds ------------------------------------------------------------

    def hold_expiry(self, placed_at: dt.datetime) -> dt.datetime:
        """Q8: expire at midnight on placement + 3, not 72 hours after."""
        if self.on("Q8"):
            return dt.datetime.combine(placed_at.date() + dt.timedelta(days=3), dt.time(0, 0, 0))
        return placed_at + dt.timedelta(hours=72)


# Which hook each quirk acts at. Used by the docs and by the reconciler's
# attribution map -- never by classification, which must not know the answer.
HOOKS: dict[str, str] = {
    "Q1": "interest_rounding",
    "Q2": "business_date_for",
    "Q3": "interest_basis",
    "Q4": "monthly_fee",
    "Q5": "is_business_day",
    "Q6": "interest_basis",
    "Q7": "min_balance_basis",
    "Q8": "hold_expiry",
    "Q9": "transform_transactions",
    "Q10": "transform_transactions",
    "Q11": "transform_transactions",
    "Q12": "interest_post_date",
}

__all__ = ["HOOKS", "QuirkSpec", "Quirks", "load_specs"]
