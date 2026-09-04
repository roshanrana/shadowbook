"""Render reports/FINDINGS.md from run artefacts.

The report is GENERATED, never hand-edited. `make report` reads only the JSON
artefacts written by the finding runs; it never touches a live system, so it can
be re-run against an old artefact and produce the same document.

Determinism is a requirement, not a nicety: two renders of the same artefacts
must be byte-identical, and a test asserts it.
"""

from __future__ import annotations

import argparse
import json
import platform
import subprocess
import sys
from pathlib import Path
from typing import Any

from jinja2 import Environment, FileSystemLoader, StrictUndefined

TEMPLATE_DIR = Path(__file__).resolve().parents[2] / "templates"
TEMPLATE_NAME = "FINDINGS.md.j2"


def git_sha(repo: Path) -> str:
    try:
        out = subprocess.run(
            ["git", "-C", str(repo), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
            timeout=10,
        )
        return out.stdout.strip()
    except (subprocess.SubprocessError, OSError):
        return "unknown"


def _best_rows(finding: dict[str, Any]) -> list[dict[str, Any]]:
    """One row per quirk: the window in which it was detected, else the first."""
    specs = finding["quirk_specs"]
    per_quirk: dict[str, Any] = finding["per_quirk"]
    rows: list[dict[str, Any]] = []

    for quirk_id in sorted(specs, key=lambda q: int(q[1:])):
        candidates = [
            (key, value)
            for key, value in sorted(per_quirk.items())
            if key.split("/")[0] == quirk_id
        ]
        if not candidates:
            continue
        chosen = None
        for key, value in candidates:
            d = value["discoveries"].get(quirk_id)
            if d and d["detected"]:
                chosen = (key, value, d)
                break
        if chosen is None:
            key, value = candidates[0]
            chosen = (key, value, value["discoveries"].get(quirk_id, {}))
        key, value, d = chosen
        rows.append(
            {
                "quirk_id": quirk_id,
                "name": specs[quirk_id]["name"],
                "cadence": specs[quirk_id]["cadence"],
                "window": key.split("/")[1],
                "detected": bool(d.get("detected")),
                "first_detected_business_day": d.get("first_detected_business_day"),
                "first_detected_grain": d.get("first_detected_grain"),
                "breaks_at_first_detection": d.get("breaks_at_first_detection"),
                "breaks_to_isolate": d.get("breaks_to_isolate"),
            }
        )
    return rows


def _median(values: list[int]) -> float:
    ordered = sorted(values)
    n = len(ordered)
    if n == 0:
        return 0.0
    mid = n // 2
    if n % 2:
        return float(ordered[mid])
    return (ordered[mid - 1] + ordered[mid]) / 2


def _attribution(rows: list[dict[str, Any]]) -> dict[str, Any]:
    """Separate 'something is wrong' from 'this is what is wrong'.

    These are different results and the per-quirk table reports them in two
    different columns, which makes the gap between them easy to miss: the
    headline count answers only the first question. Operationally the second is
    the expensive one -- a migration team that knows a break exists but not
    which behaviour caused it still has to go and find out.
    """
    detected = [r for r in rows if r["detected"]]
    isolated = [r for r in detected if r.get("breaks_to_isolate") is not None]
    days = [
        r["first_detected_business_day"]
        for r in detected
        if r.get("first_detected_business_day") is not None
    ]
    return {
        "detected": len(detected),
        "total": len(rows),
        "isolated": len(isolated),
        "isolated_ids": [r["quirk_id"] for r in isolated],
        "unattributed_ids": [r["quirk_id"] for r in detected if r not in isolated],
        "median_days": _median(days),
        "worst_days": max(days) if days else 0,
        "best_days": min(days) if days else 0,
    }


def _cadence_floor(rows: list[dict[str, Any]]) -> dict[str, Any]:
    """Group time-to-discovery by the quirk's own cadence.

    A quirk that only fires at month end cannot be discovered before month end
    however good the reconciler is, so a single median over all twelve quirks
    is a statistic about the cadence mix, not about detection sensitivity.
    Splitting them is what makes the numbers comparable.
    """
    by_cadence: dict[str, list[int]] = {}
    for r in rows:
        day = r.get("first_detected_business_day")
        if not r["detected"] or day is None:
            continue
        by_cadence.setdefault(r["cadence"], []).append(day)
    return {
        cadence: {
            "count": len(days),
            "median": _median(days),
            "min": min(days),
            "max": max(days),
        }
        for cadence, days in sorted(by_cadence.items())
    }


def _finding2_notes(rows: list[dict[str, Any]]) -> list[str]:
    """One sentence per configuration, derived from the table.

    Built here rather than in the template because Jinja whitespace control
    kept collapsing the bullets onto a single line, and because a sentence that
    states a result deserves a test.
    """
    notes: list[str] = []
    for r in rows:
        dup_min, dup_max, runs = r.get("dup_min", 0), r.get("dup_max", 0), r.get("runs", 0)
        if dup_max == 0:
            dup = f"duplicated nothing in any of its {runs} runs"
        elif dup_min > 0:
            dup = f"duplicated in **every** run ({dup_min}\u2013{dup_max})"
        else:
            dup = f"duplicated in some runs and not others (0\u2013{dup_max})"

        lost_max = r.get("lost_max", 0)
        if lost_max == 0:
            lost = "and lost nothing."
        else:
            lost = (
                f"and **lost {lost_max} movements** at worst \u2014 the only "
                "configuration here that lost anything at all."
            )
        notes.append(f"- **{r['config']}** \u2014 {dup}, {lost}")
    return notes


def build_context(
    finding1_path: Path, finding2_path: Path | None, repo: Path, generated_at: str
) -> dict[str, Any]:
    finding = json.loads(finding1_path.read_text(encoding="utf-8"))
    rows = _best_rows(finding)

    combined: dict[str, Any] = {}
    for window, row in sorted(finding["combined"].items()):
        detected = sum(1 for d in row["discoveries"].values() if d["detected"])
        combined[window] = {
            "total_breaks": row["total_breaks"],
            "business_days": row["business_days"],
            "by_classification": row["by_classification"],
            "detected_count": detected,
            "enabled_count": len(row["enabled"]),
        }

    finding2: list[dict[str, Any]] | None = None
    finding2_runs = 0
    finding2_reason = (
        "The three-broker chaos profile requires a Docker daemon, which was not "
        "available in the environment this report was generated in. Run "
        "`make up-chaos && make ablate` on a machine with Docker to populate it."
    )
    finding2_kind = "real"
    finding2_broker = ""
    finding2_exact = True
    if finding2_path is not None and finding2_path.exists():
        payload = json.loads(finding2_path.read_text(encoding="utf-8"))
        finding2 = payload.get("rows") or None
        finding2_runs = int(payload.get("runs_per_config", 0))
        finding2_kind = str(payload.get("kind", "real"))
        finding2_broker = str(payload.get("broker", ""))
        finding2_exact = bool(payload.get("exact_counts", True))
        if not finding2:
            finding2_reason = str(payload.get("reason", finding2_reason))

    return {
        "attribution": _attribution(rows),
        "cadence_floor": _cadence_floor(rows),
        "header": {
            "git_sha": git_sha(repo),
            "seed": finding["seed"],
            "windows": finding["windows"],
            "window_detail": (
                "W1 2028-02-28 to 2028-04-07 (30 business days: the leap day, two "
                "month ends, and a first-of-month falling on a Saturday) and W2 "
                "2028-10-02 to 2028-10-13 (Columbus Day)"
            ),
            "broker": (
                "Redpanda (not exercised in this run)"
                if not finding2
                else (finding2_broker or "Redpanda")
            ),
            "go_version": _go_version(),
            "python_version": platform.python_version(),
            "postgres": "PostgreSQL 16",
            "machine": f"{platform.system()} {platform.machine()}",
            "generated_at": generated_at,
        },
        "finding1": rows,
        "finding1_detected": sum(1 for r in rows if r["detected"]),
        "finding1_undetected": [r["quirk_id"] for r in rows if not r["detected"]],
        "combined": combined,
        "finding2": finding2,
        "finding2_runs": finding2_runs,
        "finding2_reason": finding2_reason,
        "finding2_notes": _finding2_notes(finding2) if finding2 else [],
        "finding2_kind": finding2_kind,
        "finding2_broker": finding2_broker,
        "finding2_exact": finding2_exact,
    }


def _go_version() -> str:
    try:
        out = subprocess.run(
            ["go", "version"], capture_output=True, text=True, check=True, timeout=10
        )
        return out.stdout.strip().split()[2]
    except (subprocess.SubprocessError, OSError, IndexError):
        return "unknown"


def render(context: dict[str, Any]) -> str:
    env = Environment(
        loader=FileSystemLoader(TEMPLATE_DIR),
        undefined=StrictUndefined,
        trim_blocks=True,
        lstrip_blocks=True,
        keep_trailing_newline=True,
        autoescape=False,
    )
    return env.get_template(TEMPLATE_NAME).render(**context)


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description="Render reports/FINDINGS.md.")
    p.add_argument("--finding1", type=Path, required=True)
    p.add_argument("--finding2", type=Path)
    p.add_argument("--repo", type=Path, default=Path.cwd())
    p.add_argument("--out", type=Path, required=True)
    p.add_argument(
        "--generated-at",
        default="deterministic build",
        help="stamped into the header; fixed by default so output is byte-identical",
    )
    args = p.parse_args(argv)

    context = build_context(args.finding1, args.finding2, args.repo, args.generated_at)
    body = render(context)
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(body, encoding="utf-8")
    sys.stdout.write(f"wrote {args.out} ({len(body.splitlines())} lines)\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
