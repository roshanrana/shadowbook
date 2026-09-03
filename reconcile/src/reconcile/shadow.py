"""Where the shadow ledger's view comes from.

Two sources, one protocol. ``LedgerShadow`` reads the Go ledger over HTTP and is
what ``make demo`` uses. ``ExtractShadow`` reads a control run of the simulator
-- every quirk disabled, which IS documented behaviour by construction -- and
lets the reconciler be exercised end to end without a running ledger.

The control-run source is not a shortcut: a test that reconciles the quirked run
against the control run is measuring exactly the thing Finding 1 measures, and
it does so without any moving parts that could fail for unrelated reasons.
"""

from __future__ import annotations

import datetime as dt
import json
import urllib.request
from collections.abc import Sequence
from pathlib import Path
from typing import Protocol

from reconcile.ingest import read_file
from reconcile.model import Movement


class ShadowSource(Protocol):
    """The shadow ledger's movements for a business date."""

    def movements_for(self, business_date: dt.date) -> Sequence[Movement]: ...


class ExtractShadow:
    """Shadow view backed by a control run of the simulator."""

    def __init__(self, extract_dir: Path) -> None:
        self.dir = extract_dir
        self._cache: dict[dt.date, tuple[Movement, ...]] = {}

    def _load_all(self) -> None:
        if self._cache:
            return
        by_date: dict[dt.date, list[Movement]] = {}
        for path in sorted(self.dir.glob("TXN_*.txt")):
            result = read_file(path)
            for m in result.movements:
                by_date.setdefault(m.business_date, []).append(m)
        self._cache = {d: tuple(sorted(ms, key=lambda m: m.txn_id)) for d, ms in by_date.items()}

    def movements_for(self, business_date: dt.date) -> Sequence[Movement]:
        self._load_all()
        return self._cache.get(business_date, ())

    def dates(self) -> list[dt.date]:
        self._load_all()
        return sorted(self._cache)


class LedgerShadow:
    """Shadow view backed by the ledger's read API."""

    def __init__(self, base_url: str, account_ids: Sequence[str], timeout: float = 10.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.account_ids = list(account_ids)
        self.timeout = timeout

    def movements_for(self, business_date: dt.date) -> Sequence[Movement]:
        out: list[Movement] = []
        for account_id in self.account_ids:
            url = (
                f"{self.base_url}/v1/accounts/{account_id}/entries"
                f"?business_date={business_date.isoformat()}"
            )
            req = urllib.request.Request(url, headers={"X-Principal": "sim"})
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                payload = json.loads(resp.read().decode("utf-8"))
            for e in payload.get("entries", []):
                out.append(
                    Movement(
                        txn_id=str(e["entry_id"]),
                        account_id=str(e["account_id"]),
                        currency="USD",
                        amount_minor=int(e["amount_minor"]),
                        business_date=business_date,
                        value_date=business_date,
                        kind="transfer",
                    )
                )
        return sorted(out, key=lambda m: m.txn_id)
