"""Run reconciliation over a window and produce Finding 1."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import sys
from collections.abc import Sequence
from dataclasses import asdict, dataclass
from pathlib import Path

from reconcile.age import BreakLedger
from reconcile.discovery import measure, render_rows
from reconcile.grains import all_grains
from reconcile.ingest import ExtractInbox, read_file
from reconcile.model import Break, Classification, IngestStatus, Movement
from reconcile.shadow import ExtractShadow, ShadowSource


@dataclass(slots=True)
class WindowReport:
    window_id: str
    business_days: int
    total_breaks: int
    by_classification: dict[str, int]
    ingest_problems: dict[str, int]
    oldest_open_age: int
    findings: list[dict[str, object]]


def load_legacy(extract_dir: Path) -> tuple[dict[dt.date, list[Movement]], dict[str, int]]:
    """Read every TXN extract, recording ingest problems rather than raising."""
    inbox = ExtractInbox()
    by_date: dict[dt.date, list[Movement]] = {}
    problems: dict[str, int] = {}

    for path in sorted(extract_dir.glob("TXN_*.txt")):
        result = inbox.offer(read_file(path), received_on=dt.date(2028, 12, 31))
        if result.status is not IngestStatus.ACCEPTED:
            problems[str(result.status)] = problems.get(str(result.status), 0) + 1
        if result.status is IngestStatus.DUPLICATE:
            # A byte-identical redelivery contributes nothing. Not double counted.
            continue
        for m in result.movements:
            by_date.setdefault(m.business_date, []).append(m)

    return {d: sorted(ms, key=lambda m: m.txn_id) for d, ms in by_date.items()}, problems


def reconcile_window(
    window_id: str,
    legacy_dir: Path,
    shadow: ShadowSource,
    enabled_quirks: Sequence[str],
    business_days: Sequence[dt.date],
    fee_amounts: frozenset[int] = frozenset(),
) -> tuple[WindowReport, list[tuple[dt.date, list[Break]]]]:
    legacy_by_date, problems = load_legacy(legacy_dir)

    ledger = BreakLedger()
    per_day: list[tuple[dt.date, list[Break]]] = []
    counts: dict[str, int] = {str(c): 0 for c in Classification}

    for d in business_days:
        legacy = legacy_by_date.get(d, [])
        shadow_movements = list(shadow.movements_for(d))
        breaks = all_grains(legacy, shadow_movements, d)
        ledger.observe(d, breaks)
        per_day.append((d, breaks))
        for b in breaks:
            counts[str(b.classification)] += 1

    discoveries = measure(per_day, enabled_quirks, window_id, fee_amounts)
    from legacy_sim.quirks import load_specs  # only for names in the report

    specs_path = Path(__file__).resolve().parents[3] / "legacy-sim" / "quirks.yaml"
    specs = load_specs(specs_path) if specs_path.exists() else {}

    report = WindowReport(
        window_id=window_id,
        business_days=len(business_days),
        total_breaks=sum(len(b) for _, b in per_day),
        by_classification=counts,
        ingest_problems=problems,
        oldest_open_age=ledger.oldest_age(),
        findings=render_rows(discoveries, dict(specs)),
    )
    return report, per_day


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description="Reconcile legacy extracts against the shadow.")
    p.add_argument("--legacy", type=Path, required=True, help="quirked extract directory")
    p.add_argument("--shadow", type=Path, required=True, help="control-run extract directory")
    p.add_argument("--window", default="W1")
    p.add_argument("--out", type=Path, help="write the report JSON here")
    args = p.parse_args(argv)

    shadow = ExtractShadow(args.shadow)
    legacy_by_date, _ = load_legacy(args.legacy)
    days = sorted(set(legacy_by_date) | set(shadow.dates()))

    from legacy_sim.generator import PRODUCTS
    from legacy_sim.quirks import load_specs

    specs_path = Path(__file__).resolve().parents[3] / "legacy-sim" / "quirks.yaml"
    enabled = sorted(q for q, s in load_specs(specs_path).items() if s.enabled)
    fees = frozenset(
        int(v["monthly_fee_minor"]) for v in PRODUCTS.values() if int(v["monthly_fee_minor"])
    ) | frozenset(
        int(v["min_balance_fee_minor"])
        for v in PRODUCTS.values()
        if int(v["min_balance_fee_minor"])
    )

    report, _ = reconcile_window(args.window, args.legacy, shadow, enabled, days, fees)
    blob = json.dumps(asdict(report), indent=2, sort_keys=True, default=str)
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(blob + "\n", encoding="utf-8")
    sys.stdout.write(blob + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
