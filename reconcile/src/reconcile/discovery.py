"""Quirk attribution and time-to-discovery (FR-R3) -- Finding 1.

Attribution is SEPARATE from classification and never feeds back into it. The
classifier in :mod:`reconcile.classify` does not know quirks exist; this module
maps the signatures it produced onto candidate quirks afterwards. Keeping them
apart is what stops the reconciler from knowing the answer in advance.

Three outcomes are reported, not two:

* **isolated** -- the signature narrowed to exactly one quirk;
* **surfaced but not isolated** -- breaks appeared and were attributed to a set
  that never narrowed to one;
* **undetected** -- nothing appeared at all.

Undetected quirks are the most important rows in the table and are shown, never
hidden (the ``findings-report`` skill says so).
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence
from dataclasses import dataclass, field

from reconcile.model import Break, Grain

# Signature -> the quirks that could have produced it. A signature mapping to
# more than one quirk is honest: two quirks really can look alike at first, and
# "breaks to isolate" measures how long it takes to tell them apart.
SIGNATURE_CANDIDATES: dict[str, frozenset[str]] = {
    # Rounding-shaped: at most one minor unit. Q1 (half-up vs half-even) and
    # Q10 (JPY truncation) both look like this at first sight, which is why
    # "breaks to isolate" is not always 1.
    "round:1": frozenset({"Q1", "Q10"}),
    "scale:100": frozenset({"Q10"}),
    # Day-count bases. Each ratio names exactly one quirk, so these isolate
    # on the first break that carries them.
    "basis:366_360": frozenset({"Q3"}),
    "basis:365_360": frozenset({"Q3"}),
    "basis:366_365": frozenset({"Q6"}),
    # A record on one side only, with no counterpart anywhere in the window.
    "missing:shadow": frozenset({"Q9", "Q11"}),
    "missing:legacy": frozenset({"Q9", "Q11"}),
    # A whole account-day booked by one side and not the other.
    "account_day_missing:shadow": frozenset({"Q2", "Q5", "Q12"}),
    "account_day_missing:legacy": frozenset({"Q2", "Q5", "Q12"}),
    # A whole BUSINESS DAY booked by one side and not the other. Only a
    # calendar disagreement can do that.
    "control_total_missing:shadow": frozenset({"Q5"}),
    "control_total_missing:legacy": frozenset({"Q5"}),
    # A day short by exactly one whole transaction.
    "txn:whole": frozenset({"Q9", "Q11"}),
    # Available balance differs while the ledger balance agrees: a hold, and
    # nothing else, behaves that way.
    "hold:expiry": frozenset({"Q8"}),
}

# A timing signature carries its offset, so it is matched by prefix.
TIMING_CANDIDATES: frozenset[str] = frozenset({"Q2", "Q5", "Q12"})

# A delta equal to a configured fee amount.
FEE_CANDIDATES: frozenset[str] = frozenset({"Q1", "Q4", "Q7"})

# Balance-grain signatures carry this prefix; strip it before attributing, or
# every balance break becomes unattributable.
BALANCE_PREFIX = "balance:ledger:"


def candidates_for(b: Break, fee_amounts: frozenset[int] = frozenset()) -> frozenset[str]:
    """Which quirks could have produced this break.

    Nothing here consults which quirks are enabled -- this maps a signature the
    classifier produced onto the quirks that could explain it, and no further.
    """
    signature = b.signature.removeprefix(BALANCE_PREFIX)
    if signature.startswith("timing:"):
        return TIMING_CANDIDATES
    if signature.startswith("fee:"):
        return FEE_CANDIDATES
    if signature.startswith("unexplained:") and abs(b.delta_minor) in fee_amounts:
        return FEE_CANDIDATES
    return SIGNATURE_CANDIDATES.get(signature, frozenset())


@dataclass(slots=True)
class QuirkDiscovery:
    """One row of the Finding 1 table."""

    quirk_id: str
    window_id: str
    detected: bool = False
    first_detected_business_day: int | None = None
    first_detected_date: dt.date | None = None
    first_detected_grain: Grain | None = None
    breaks_at_first_detection: int | None = None
    breaks_to_isolate: int | None = None
    isolated: bool = False
    signatures: set[str] = field(default_factory=set)


def measure(
    breaks_by_day: Sequence[tuple[dt.date, Sequence[Break]]],
    enabled_quirks: Sequence[str],
    window_id: str,
    fee_amounts: frozenset[int] = frozenset(),
) -> dict[str, QuirkDiscovery]:
    """Compute time-to-discovery for every enabled quirk.

    ``breaks_by_day`` must be in business-day order; day 1 is the first.
    """
    out = {q: QuirkDiscovery(quirk_id=q, window_id=window_id) for q in sorted(enabled_quirks)}
    # Running tally of how many breaks have carried each quirk as a candidate.
    seen_with: dict[str, int] = dict.fromkeys(out, 0)

    for day_number, (business_date, breaks) in enumerate(breaks_by_day, start=1):
        open_today = len(breaks)
        for b in breaks:
            cands = candidates_for(b, fee_amounts)
            for q in cands:
                if q not in out:
                    continue
                row = out[q]
                seen_with[q] += 1
                row.signatures.add(b.signature)
                if not row.detected:
                    row.detected = True
                    row.first_detected_business_day = day_number
                    row.first_detected_date = business_date
                    row.first_detected_grain = b.grain
                    row.breaks_at_first_detection = open_today
                # Isolated the moment a signature points at exactly one quirk.
                if len(cands) == 1 and not row.isolated:
                    row.isolated = True
                    row.breaks_to_isolate = seen_with[q]
    return out


def render_rows(
    discoveries: dict[str, QuirkDiscovery], specs: dict[str, object]
) -> list[dict[str, object]]:
    """Flatten to sortable rows for the report. Undetected quirks are included."""
    rows: list[dict[str, object]] = []
    for quirk_id in sorted(discoveries, key=lambda q: int(q[1:])):
        d = discoveries[quirk_id]
        spec = specs.get(quirk_id)
        rows.append(
            {
                "quirk_id": quirk_id,
                "name": getattr(spec, "name", ""),
                "cadence": getattr(spec, "cadence", ""),
                "expected_grain": getattr(spec, "expected_grain", ""),
                "window": d.window_id,
                "detected": d.detected,
                "first_detected_business_day": d.first_detected_business_day,
                "first_detected_date": d.first_detected_date.isoformat()
                if d.first_detected_date
                else None,
                "first_detected_grain": str(d.first_detected_grain)
                if d.first_detected_grain
                else None,
                "breaks_at_first_detection": d.breaks_at_first_detection,
                "breaks_to_isolate": d.breaks_to_isolate,
                "isolated": d.isolated,
                "signatures": sorted(d.signatures),
            }
        )
    return rows
