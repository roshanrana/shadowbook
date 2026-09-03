"""Finding 1: time-to-discovery per quirk.

Every quirk is measured in ISOLATION -- the simulator runs with exactly that one
quirk enabled and is reconciled against the control run, in which every quirk is
off. That is a controlled experiment, and it is the only way the number means
what the finding claims: "how many business days of reconciliation until THIS
behaviour surfaces".

A combined run, with all twelve on at once, is measured too and reported
alongside. The two disagree, and the disagreement is itself a result: quirks
compound. Q2, Q9 and Q11 change which transactions land on which day, so the
daily balances Q3 and Q6 accrue interest on are no longer the shadow's, and the
exact basis ratio those two would otherwise show is destroyed. A reconciliation
engine that only ever sees the compounded case will under-attribute, and saying
so is more useful than hiding it.
"""

from __future__ import annotations

import copy
import datetime as dt
import json
import sys
from dataclasses import dataclass
from pathlib import Path

from legacy_sim.calendar import Calendar
from legacy_sim.extracts import render_bal_extract, render_txn_extract, write_extract
from legacy_sim.generator import PRODUCTS, LegacyCore
from legacy_sim.quirks import Quirks, load_specs
from legacy_sim.windows import WINDOWS, Window
from reconcile.age import BreakLedger
from reconcile.discovery import measure
from reconcile.grains import all_grains, balance_grain
from reconcile.ingest import parse_balances, read_file
from reconcile.model import Break, Classification, Movement

FEE_AMOUNTS = frozenset(
    int(v["monthly_fee_minor"]) for v in PRODUCTS.values() if int(v["monthly_fee_minor"])
) | frozenset(
    int(v["min_balance_fee_minor"]) for v in PRODUCTS.values() if int(v["min_balance_fee_minor"])
)


@dataclass(slots=True)
class RunOutput:
    movements: dict[dt.date, list[Movement]]
    balances: dict[dt.date, dict[tuple[str, str], tuple[int, int]]]


def simulate(
    window: Window, seed: int, quirks_path: Path, enabled: set[str], out_dir: Path
) -> RunOutput:
    """Run the simulator with exactly ``enabled`` switched on."""
    cal = Calendar()
    specs = copy.deepcopy(load_specs(quirks_path))
    for qid, spec in specs.items():
        object.__setattr__(spec, "enabled", qid in enabled)

    quirks = Quirks(specs, cal)
    accounts = LegacyCore.build_accounts(seed)
    core = LegacyCore(accounts=accounts, cal=cal, quirks=quirks, base_seed=seed)

    movements: dict[dt.date, list[Movement]] = {}
    balances: dict[dt.date, dict[tuple[str, str], tuple[int, int]]] = {}
    for day in core.run_window(window):
        txn_body = render_txn_extract(day.transactions, day.business_date, seed)
        bal_body = render_bal_extract(day.balances, day.business_date, seed)
        write_extract(out_dir, "TXN", day.business_date, txn_body)
        write_extract(out_dir, "BAL", day.business_date, bal_body)
        for m in read_file(_p(out_dir, "TXN", day.business_date)).movements:
            movements.setdefault(m.business_date, []).append(m)
        balances[day.business_date] = parse_balances(bal_body)
    return RunOutput(
        movements={d: sorted(ms, key=lambda m: m.txn_id) for d, ms in movements.items()},
        balances=balances,
    )


def _p(directory: Path, kind: str, d: dt.date) -> Path:
    return directory / f"{kind}_{d.strftime('%Y%m%d')}_001.txt"


def reconcile(
    legacy: RunOutput, shadow: RunOutput, window: Window, enabled: set[str], window_id: str
) -> tuple[dict[str, object], list[Break]]:
    """Reconcile one legacy run against the control, over the window's days."""
    # Reconcile every day EITHER side reported, within the window span -- not
    # the documented calendar's business days.
    #
    # Q5 is the legacy core working through Columbus Day. Iterating the
    # documented calendar means never reconciling 2028-10-09 at all, so the one
    # day Q5 exists on is skipped and it reports as undetected. In practice you
    # reconcile every day the core sends you a file, which is what this does.
    reported = set(legacy.movements) | set(shadow.movements)
    reported |= set(legacy.balances) | set(shadow.balances)
    days = sorted(d for d in reported if window.start <= d <= window.end)

    legacy_all = [m for ms in legacy.movements.values() for m in ms]
    shadow_all = [m for ms in shadow.movements.values() for m in ms]

    ledger = BreakLedger()
    per_day: list[tuple[dt.date, list[Break]]] = []
    counts = {str(c): 0 for c in Classification}
    every: list[Break] = []

    for d in days:
        breaks = all_grains(
            legacy_all,
            shadow_all,
            d,
            legacy_window=legacy_all,
            shadow_window=shadow_all,
            fee_amounts=FEE_AMOUNTS,
        )
        breaks += balance_grain(
            legacy.balances.get(d, {}), shadow.balances.get(d, {}), d, FEE_AMOUNTS
        )
        ledger.observe(d, breaks)
        per_day.append((d, breaks))
        every.extend(breaks)
        for b in breaks:
            counts[str(b.classification)] += 1

    discoveries = measure(per_day, sorted(enabled), window_id, FEE_AMOUNTS)
    summary: dict[str, object] = {
        "window": window_id,
        "business_days": len(days),
        "enabled": sorted(enabled),
        "total_breaks": len(every),
        "by_classification": counts,
        "oldest_open_age": ledger.oldest_age(),
        "discoveries": {
            q: {
                "detected": d0.detected,
                "first_detected_business_day": d0.first_detected_business_day,
                "first_detected_date": d0.first_detected_date.isoformat()
                if d0.first_detected_date
                else None,
                "first_detected_grain": str(d0.first_detected_grain)
                if d0.first_detected_grain
                else None,
                "breaks_at_first_detection": d0.breaks_at_first_detection,
                "breaks_to_isolate": d0.breaks_to_isolate,
                "isolated": d0.isolated,
                "signatures": sorted(d0.signatures),
            }
            for q, d0 in discoveries.items()
        },
    }
    return summary, every


def run(seed: int, quirks_path: Path, workdir: Path) -> dict[str, object]:
    """Produce the whole of Finding 1."""
    specs = load_specs(quirks_path)
    all_enabled = {q for q, s in specs.items() if s.enabled}

    per_quirk: dict[str, dict[str, object]] = {}
    combined: dict[str, dict[str, object]] = {}

    for window in WINDOWS:
        control = simulate(window, seed, quirks_path, set(), workdir / window.window_id / "control")

        for quirk_id in sorted(all_enabled, key=lambda q: int(q[1:])):
            legacy = simulate(
                window, seed, quirks_path, {quirk_id}, workdir / window.window_id / quirk_id
            )
            summary, _ = reconcile(legacy, control, window, {quirk_id}, window.window_id)
            key = f"{quirk_id}/{window.window_id}"
            per_quirk[key] = summary

        legacy_all = simulate(
            window, seed, quirks_path, all_enabled, workdir / window.window_id / "all"
        )
        combined[window.window_id], _ = reconcile(
            legacy_all, control, window, all_enabled, window.window_id
        )

    return {
        "seed": seed,
        "windows": [w.window_id for w in WINDOWS],
        "per_quirk": per_quirk,
        "combined": combined,
        "quirk_specs": {
            q: {
                "name": s.name,
                "cadence": s.cadence,
                "expected_grain": s.expected_grain,
                "legacy": s.legacy,
                "documented": s.documented,
            }
            for q, s in specs.items()
        },
    }


def main(argv: list[str] | None = None) -> int:
    import argparse

    p = argparse.ArgumentParser(description="Produce Finding 1.")
    p.add_argument("--seed", type=int, default=20260903)
    p.add_argument("--quirks", type=Path, required=True)
    p.add_argument("--workdir", type=Path, required=True)
    p.add_argument("--out", type=Path)
    args = p.parse_args(argv)

    result = run(args.seed, args.quirks, args.workdir)
    blob = json.dumps(result, indent=2, sort_keys=True, default=str)
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(blob + "\n", encoding="utf-8")
    sys.stdout.write(blob + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
