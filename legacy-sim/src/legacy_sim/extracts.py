"""End-of-day flat extracts: header, detail, trailer (FR-S3).

The wire format is the text file, not a schema -- a golden test pins it
byte-for-byte. Real EOD extracts look like this, and the reconciler needs a
realistic ingest problem: a trailer that can disagree with the body, a file that
can arrive twice, and a file that can be truncated mid-write.

The trailer's control total is a PER-CURRENCY signed sum. FR-S1 requires at
least two currencies, so a single scalar total would be meaningless.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Iterable
from dataclasses import dataclass
from pathlib import Path

from legacy_sim.model import AccountDayBalance, Txn

MAGIC = "SHADOWBOOK"
SEP = "|"

# Files are written with \n, UTF-8, and a trailing newline, with no locale
# dependence anywhere -- byte-identical output is FR-S6.
NEWLINE = "\n"
ENCODING = "utf-8"


@dataclass(frozen=True, slots=True)
class Trailer:
    record_count: int
    control_total: dict[str, int]

    def render(self) -> str:
        parts = ";".join(f"{cur}:{total}" for cur, total in sorted(self.control_total.items()))
        return SEP.join(["TRL", str(self.record_count), parts])


def _header(extract_type: str, business_date: dt.date, sequence: int, seed: int) -> str:
    return SEP.join(
        [
            "HDR",
            MAGIC,
            extract_type,
            business_date.strftime("%Y%m%d"),
            f"{sequence:03d}",
            str(seed),
        ]
    )


def _txn_detail(t: Txn) -> str:
    return SEP.join(
        [
            "DTL",
            t.txn_id,
            t.account_id,
            t.currency,
            str(t.amount_minor),
            str(t.scale),
            t.posted_at.isoformat(timespec="milliseconds"),
            t.business_date.isoformat(),
            t.value_date.isoformat(),
            t.kind,
        ]
    )


def _bal_detail(b: AccountDayBalance) -> str:
    return SEP.join(
        [
            "DTL",
            b.account_id,
            b.currency,
            str(b.ledger_minor),
            str(b.available_minor),
        ]
    )


def _control_total(rows: Iterable[tuple[str, int]]) -> dict[str, int]:
    out: dict[str, int] = {}
    for currency, minor in rows:
        out[currency] = out.get(currency, 0) + minor
    return out


def render_txn_extract(
    txns: list[Txn], business_date: dt.date, seed: int, sequence: int = 1
) -> str:
    """Render a TXN extract. Rows are sorted so the bytes are deterministic."""
    ordered = sorted(txns, key=lambda t: (t.posted_at, t.txn_id))
    lines = [_header("TXN", business_date, sequence, seed)]
    lines.extend(_txn_detail(t) for t in ordered)
    trailer = Trailer(
        record_count=len(ordered),
        control_total=_control_total((t.currency, t.amount_minor) for t in ordered),
    )
    lines.append(trailer.render())
    return NEWLINE.join(lines) + NEWLINE


def render_bal_extract(
    balances: list[AccountDayBalance], business_date: dt.date, seed: int, sequence: int = 1
) -> str:
    """Render a BAL extract."""
    ordered = sorted(balances, key=lambda b: b.account_id)
    lines = [_header("BAL", business_date, sequence, seed)]
    lines.extend(_bal_detail(b) for b in ordered)
    trailer = Trailer(
        record_count=len(ordered),
        control_total=_control_total((b.currency, b.ledger_minor) for b in ordered),
    )
    lines.append(trailer.render())
    return NEWLINE.join(lines) + NEWLINE


def extract_path(
    directory: Path, extract_type: str, business_date: dt.date, sequence: int = 1
) -> Path:
    return directory / f"{extract_type}_{business_date.strftime('%Y%m%d')}_{sequence:03d}.txt"


def write_extract(
    directory: Path, extract_type: str, business_date: dt.date, body: str, sequence: int = 1
) -> Path:
    directory.mkdir(parents=True, exist_ok=True)
    path = extract_path(directory, extract_type, business_date, sequence)
    with path.open("w", encoding=ENCODING, newline=NEWLINE) as fh:
        fh.write(body)
    return path


def truncate_extract(body: str, keep_fraction: float = 0.6) -> str:
    """Return a truncated copy: detail rows cut short, trailer left claiming the
    original count.

    This is the named adversarial scenario "truncated extract with bad trailer".
    The trailer is deliberately NOT recomputed -- a truncated file whose trailer
    agreed with its body would be indistinguishable from a short day.
    """
    lines = body.rstrip(NEWLINE).split(NEWLINE)
    header, details, trailer = lines[0], lines[1:-1], lines[-1]
    keep = max(1, int(len(details) * keep_fraction))
    return NEWLINE.join([header, *details[:keep], trailer]) + NEWLINE
