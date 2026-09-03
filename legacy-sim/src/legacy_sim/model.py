"""Value types shared across the simulator.

Money is integer minor units with an explicit currency and scale, exactly as on
the Go side (FR-L11). Nothing here uses ``float``.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass, replace

# Scale is a property of the currency, not of the caller. The shadow ledger's
# Go registry says the same thing; Q10 is the legacy core storing JPY at scale 2
# and truncating, which the simulator reproduces deliberately.
SCALES: dict[str, int] = {"USD": 2, "EUR": 2, "JPY": 0}


def scale_of(currency: str) -> int:
    try:
        return SCALES[currency]
    except KeyError as exc:
        raise ValueError(f"unknown currency {currency!r}") from exc


@dataclass(frozen=True, slots=True)
class Account:
    account_id: str
    product_code: str
    currency: str
    opened_on: dt.date


@dataclass(frozen=True, slots=True)
class Txn:
    """One legacy transaction.

    ``posted_at`` is a wall-clock instant; ``business_date`` is what the legacy
    core decided that instant belongs to. Q2 is the two disagreeing.
    """

    txn_id: str
    account_id: str
    currency: str
    amount_minor: int
    posted_at: dt.datetime
    value_date: dt.date
    business_date: dt.date
    kind: str
    counterparty: str = ""
    # For JPY only: the value in HUNDREDTHS of a yen that the core actually
    # computed. The documented ledger rounds this half-even to whole yen; Q10 is
    # the legacy core storing it in a scale-2 column and TRUNCATING instead.
    # None for currencies whose amounts are exact at their own scale.
    true_hundredths: int | None = None

    @property
    def scale(self) -> int:
        return scale_of(self.currency)

    def with_business_date(self, d: dt.date) -> Txn:
        return replace(self, business_date=d)

    def with_amount(self, minor: int) -> Txn:
        return replace(self, amount_minor=minor)


@dataclass(frozen=True, slots=True)
class Hold:
    hold_id: str
    account_id: str
    currency: str
    amount_minor: int
    placed_at: dt.datetime
    expires_at: dt.datetime


@dataclass(frozen=True, slots=True)
class AccountDayBalance:
    account_id: str
    currency: str
    business_date: dt.date
    ledger_minor: int
    available_minor: int
