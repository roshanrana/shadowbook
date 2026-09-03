"""Reading legacy extracts, including the ones that arrive wrong (FR-R4).

Late, redelivered and truncated extracts are handled HERE, before comparison, so
a bad file produces a recorded break rather than a crash or a double count. The
identity is ``(extract_type, business_date, sequence)``, which is what makes a
redelivery idempotent.
"""

from __future__ import annotations

import datetime as dt
import hashlib
from pathlib import Path

from reconcile.model import IngestResult, IngestStatus, Movement

SEP = "|"
MAGIC = "SHADOWBOOK"


class ExtractFormatError(ValueError):
    """The file is not an extract at all. Distinct from a bad trailer, which is
    a data problem the reconciler records rather than raises."""


def parse_control_total(field: str) -> dict[str, int]:
    """Parse ``USD:-1250000;JPY:0`` into a per-currency map."""
    out: dict[str, int] = {}
    if not field:
        return out
    for part in field.split(";"):
        currency, _, total = part.partition(":")
        if not currency or not total:
            raise ExtractFormatError(f"malformed control total segment {part!r}")
        out[currency] = int(total)
    return out


def parse(body: str) -> IngestResult:
    """Parse an extract body. Never raises on a *data* problem -- only on a file
    that is structurally not an extract."""
    sha = hashlib.sha256(body.encode("utf-8")).hexdigest()
    lines = [ln for ln in body.split("\n") if ln]
    if not lines:
        raise ExtractFormatError("empty extract")

    header = lines[0].split(SEP)
    if len(header) != 6 or header[0] != "HDR" or header[1] != MAGIC:
        raise ExtractFormatError(f"bad header: {lines[0]!r}")
    extract_type = header[2]
    business_date = dt.datetime.strptime(header[3], "%Y%m%d").date()
    sequence = int(header[4])

    trailer_line = lines[-1]
    if not trailer_line.startswith("TRL" + SEP):
        # A file cut off before its trailer was written. Recorded, not raised.
        movements = tuple(_movements(extract_type, lines[1:]))
        return IngestResult(
            extract_type=extract_type,
            business_date=business_date,
            sequence=sequence,
            status=IngestStatus.TRUNCATED,
            record_count=len(movements),
            declared_count=-1,
            control_total=_totals(movements),
            declared_control_total={},
            movements=movements,
            sha256=sha,
        )

    trailer = trailer_line.split(SEP)
    declared_count = int(trailer[1])
    declared_total = parse_control_total(trailer[2] if len(trailer) > 2 else "")

    movements = tuple(_movements(extract_type, lines[1:-1]))
    actual_total = _totals(movements)

    status = IngestStatus.ACCEPTED
    if len(movements) != declared_count:
        # The body and the trailer disagree: the file is short, or padded.
        status = (
            IngestStatus.TRUNCATED
            if len(movements) < declared_count
            else IngestStatus.TRAILER_MISMATCH
        )
    elif actual_total != declared_total:
        status = IngestStatus.TRAILER_MISMATCH

    return IngestResult(
        extract_type=extract_type,
        business_date=business_date,
        sequence=sequence,
        status=status,
        record_count=len(movements),
        declared_count=declared_count,
        control_total=actual_total,
        declared_control_total=declared_total,
        movements=movements,
        sha256=sha,
    )


def parse_balances(body: str) -> dict[tuple[str, str], tuple[int, int]]:
    """Read a BAL extract into ``(account, currency) -> (ledger, available)``.

    Available balance is where a hold lives, and holds never touch the ledger
    balance -- so this is the only surface on which Q8 can appear at all.
    """
    out: dict[tuple[str, str], tuple[int, int]] = {}
    for ln in body.split("\n"):
        if not ln.startswith("DTL" + SEP):
            continue
        f = ln.split(SEP)
        if len(f) != 5:
            continue
        out[(f[1], f[2])] = (int(f[3]), int(f[4]))
    return out


def _movements(extract_type: str, detail_lines: list[str]) -> list[Movement]:
    if extract_type != "TXN":
        return []
    out: list[Movement] = []
    for ln in detail_lines:
        f = ln.split(SEP)
        if f[0] != "DTL":
            raise ExtractFormatError(f"unexpected record type {f[0]!r}")
        if len(f) != 10:
            # A row cut off mid-write. Skip it; the trailer check will notice
            # the count is short and mark the file truncated.
            continue
        out.append(
            Movement(
                txn_id=f[1],
                account_id=f[2],
                currency=f[3],
                amount_minor=int(f[4]),
                # DTL|txn_id|account|ccy|amount|scale|posted_at|business_date|value_date|kind
                business_date=dt.date.fromisoformat(f[7]),
                value_date=dt.date.fromisoformat(f[8]),
                kind=f[9],
            )
        )
    return out


def _totals(movements: tuple[Movement, ...] | list[Movement]) -> dict[str, int]:
    out: dict[str, int] = {}
    for m in movements:
        out[m.currency] = out.get(m.currency, 0) + m.amount_minor
    return out


class ExtractInbox:
    """Tracks which extracts have been seen, so a redelivery is not double counted."""

    def __init__(self) -> None:
        self._seen: dict[tuple[str, dt.date, int], str] = {}

    def offer(self, result: IngestResult, received_on: dt.date) -> IngestResult:
        """Record an extract, downgrading its status when it is a repeat or late.

        A byte-identical redelivery is a DUPLICATE and contributes nothing. A
        redelivery with different bytes keeps its own status and is compared --
        a core that re-sends a corrected file is a normal event, not an error.
        """
        import dataclasses

        key = (result.extract_type, result.business_date, result.sequence)
        previous = self._seen.get(key)
        if previous is not None and previous == result.sha256:
            return dataclasses.replace(result, status=IngestStatus.DUPLICATE)
        self._seen[key] = result.sha256
        if previous is None and received_on > result.business_date:
            return dataclasses.replace(result, status=IngestStatus.LATE)
        return result

    def seen(self) -> int:
        return len(self._seen)


def read_file(path: Path) -> IngestResult:
    return parse(path.read_text(encoding="utf-8"))
