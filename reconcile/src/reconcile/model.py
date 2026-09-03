"""Value types for reconciliation. Integers only -- never ``float``."""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from enum import StrEnum


class Grain(StrEnum):
    """The three comparison grains of FR-R1."""

    TRANSACTION = "transaction"
    ACCOUNT_DAY = "account_day"
    CONTROL_TOTAL = "control_total"


class Classification(StrEnum):
    """The three deterministic break classes of FR-R2."""

    TIMING = "timing"
    MODEL_DIFFERENCE = "model_difference"
    DEFECT = "defect"


class IngestStatus(StrEnum):
    ACCEPTED = "accepted"
    TRAILER_MISMATCH = "trailer_mismatch"
    TRUNCATED = "truncated"
    DUPLICATE = "duplicate"
    LATE = "late"


@dataclass(frozen=True, slots=True)
class Movement:
    """One side's view of a single transaction."""

    txn_id: str
    account_id: str
    currency: str
    amount_minor: int
    business_date: dt.date
    value_date: dt.date
    kind: str


@dataclass(frozen=True, slots=True)
class Break:
    """One reconciliation break.

    ``signature`` is a deterministic label for WHY the two sides differ. It
    drives quirk attribution, and is deliberately computed without any knowledge
    of which quirks are enabled.
    """

    grain: Grain
    business_date: dt.date
    currency: str
    delta_minor: int
    classification: Classification
    signature: str
    account_id: str | None = None
    txn_id: str | None = None
    legacy_minor: int | None = None
    shadow_minor: int | None = None

    @property
    def key(self) -> tuple[str, str, str, str, str]:
        """Stable identity, matching the breaks_identity unique index."""
        return (
            str(self.grain),
            self.business_date.isoformat(),
            self.account_id or "",
            self.txn_id or "",
            self.currency,
        )


@dataclass(frozen=True, slots=True)
class IngestResult:
    extract_type: str
    business_date: dt.date
    sequence: int
    status: IngestStatus
    record_count: int
    declared_count: int
    control_total: dict[str, int]
    declared_control_total: dict[str, int]
    movements: tuple[Movement, ...]
    sha256: str
