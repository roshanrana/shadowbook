"""NFR-7 coverage gate. Reads `go tool cover -func` output on stdin.

Coverage is enforced per GROUP, not per package, because the targets in the
requirements are about paths through the system rather than about files.
"""

from __future__ import annotations

import collections
import re
import sys

TARGETS: dict[str, tuple[float, tuple[str, ...]]] = {
    # The whole ledger, its store, and the invariants that live in DDL.
    "ledger": (
        85.0,
        ("internal/ledger", "internal/money", "internal/bizdate", "internal/broker", "migrations"),
    ),
    # The write path and the two value types it rests on. Migrations are
    # excluded here: the DDL is exercised by the invariant tests, but it is
    # schema rather than posting-path code and averaging it in measures the
    # wrong thing.
    "posting path": (
        95.0,
        ("internal/ledger/posting", "internal/money", "internal/bizdate"),
    ),
}


def main() -> int:
    agg: dict[str, list[float]] = collections.defaultdict(lambda: [0.0, 0.0])
    for line in sys.stdin:
        m = re.match(r"^(\S+\.go):\d+:\s+\S+\s+([\d.]+)%$", line)
        if not m:
            continue
        pkg = "/".join(m.group(1).split("/")[:-1]).replace("github.com/roshanrana/shadowbook/", "")
        agg[pkg][0] += float(m.group(2))
        agg[pkg][1] += 1

    if not agg:
        print("coverage: no data on stdin", file=sys.stderr)
        return 1

    print(f"{'package':<36} {'cover':>7}")
    for pkg, (total, n) in sorted(agg.items()):
        print(f"{pkg:<36} {total / n:>6.1f}%")

    failed = False
    print()
    for name, (threshold, prefixes) in TARGETS.items():
        total = n = 0.0
        for pkg, (t, c) in agg.items():
            if any(pkg == p or pkg.startswith(p + "/") for p in prefixes):
                total += t
                n += c
        pct = total / n if n else 0.0
        status = "ok" if pct >= threshold else "FAIL"
        print(f"{name:<16} {pct:6.1f}%  (target >= {threshold}%)  {status}")
        failed = failed or pct < threshold
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
