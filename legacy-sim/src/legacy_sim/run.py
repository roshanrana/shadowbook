"""Run the simulator over the configured windows and write the extracts."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import sys
from pathlib import Path

from legacy_sim.calendar import Calendar
from legacy_sim.extracts import render_bal_extract, render_txn_extract, write_extract
from legacy_sim.generator import LegacyCore
from legacy_sim.quirks import load_specs
from legacy_sim.windows import WINDOWS, Window, assert_all_quirks_reachable

DEFAULT_SEED = 20260903


def run_window(
    w: Window, seed: int, quirks_path: Path, out_dir: Path, documented_only: bool = False
) -> dict[str, object]:
    """Run one window, writing TXN and BAL extracts per business day.

    ``documented_only`` disables every quirk, producing the control run in which
    the legacy core and the shadow agree exactly.
    """
    cal = Calendar()
    specs = load_specs(quirks_path)
    if documented_only:
        for spec in specs.values():
            object.__setattr__(spec, "enabled", False)

    from legacy_sim.quirks import Quirks

    quirks = Quirks(specs, cal)
    accounts = LegacyCore.build_accounts(seed)
    core = LegacyCore(accounts=accounts, cal=cal, quirks=quirks, base_seed=seed)

    window_dir = out_dir / w.window_id
    days = 0
    txn_count = 0
    for day in core.run_window(w):
        write_extract(
            window_dir,
            "TXN",
            day.business_date,
            render_txn_extract(day.transactions, day.business_date, seed),
        )
        write_extract(
            window_dir,
            "BAL",
            day.business_date,
            render_bal_extract(day.balances, day.business_date, seed),
        )
        days += 1
        txn_count += len(day.transactions)

    return {
        "window": w.window_id,
        "start": w.start.isoformat(),
        "end": w.end.isoformat(),
        "business_days": days,
        "transactions": txn_count,
        "accounts": len(accounts),
        "quirks_enabled": sorted(quirks.enabled_ids()),
        "extract_dir": str(window_dir),
    }


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description="Run the SHADOWBOOK legacy simulator.")
    p.add_argument("--seed", type=int, default=DEFAULT_SEED)
    p.add_argument("--quirks", type=Path, required=True)
    p.add_argument("--out", type=Path, required=True)
    p.add_argument("--window", choices=[w.window_id for w in WINDOWS] + ["all"], default="all")
    p.add_argument(
        "--documented-only",
        action="store_true",
        help="disable every quirk: the control run in which legacy and shadow agree",
    )
    args = p.parse_args(argv)

    cal = Calendar()
    specs = load_specs(args.quirks)
    enabled = {q for q, s in specs.items() if s.enabled}
    if not args.documented_only:
        # The guard for HLD risk R1: refuse to run if a quirk could not fire in
        # any window, because Finding 1 would then under-report silently.
        assert_all_quirks_reachable(cal, enabled)

    windows = (
        WINDOWS if args.window == "all" else tuple(w for w in WINDOWS if w.window_id == args.window)
    )
    summary = [
        run_window(w, args.seed, args.quirks, args.out, args.documented_only) for w in windows
    ]
    json.dump(
        {"seed": args.seed, "generated": dt.date(2028, 1, 1).isoformat(), "windows": summary},
        sys.stdout,
        indent=2,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
