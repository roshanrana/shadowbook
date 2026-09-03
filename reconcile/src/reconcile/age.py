"""Break ageing: open, carry, close (FR-R2)."""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence
from dataclasses import dataclass, field

from reconcile.model import Break


@dataclass(slots=True)
class AgedBreak:
    break_: Break
    first_seen_on: dt.date
    last_seen_on: dt.date
    closed_on: dt.date | None = None
    age_business_days: int = 0


@dataclass(slots=True)
class BreakLedger:
    """Tracks breaks across business days so ageing is a fact, not a guess."""

    open_breaks: dict[tuple[str, str, str, str, str], AgedBreak] = field(default_factory=dict)
    closed: list[AgedBreak] = field(default_factory=list)

    def observe(self, business_date: dt.date, breaks: Sequence[Break]) -> None:
        """Record one day's breaks, ageing those that persist and closing those
        that stopped appearing."""
        today = {b.key: b for b in breaks}

        for key, b in today.items():
            existing = self.open_breaks.get(key)
            if existing is None:
                self.open_breaks[key] = AgedBreak(
                    break_=b, first_seen_on=business_date, last_seen_on=business_date
                )
            else:
                existing.last_seen_on = business_date
                existing.age_business_days += 1

        for key in list(self.open_breaks):
            if key in today:
                continue
            aged = self.open_breaks.pop(key)
            aged.closed_on = business_date
            self.closed.append(aged)

    def snapshot(self) -> list[AgedBreak]:
        """Everything currently open, in a deterministic order."""
        return [self.open_breaks[k] for k in sorted(self.open_breaks)]

    def oldest_age(self) -> int:
        return max((a.age_business_days for a in self.open_breaks.values()), default=0)
