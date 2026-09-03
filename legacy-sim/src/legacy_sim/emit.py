"""Both ingress paths into the shadow ledger (FR-S4, D-012).

HTTP drives ``make demo`` and Finding 1: it exercises idempotency-by-constraint
on the path a real migration would use for replay, and a reviewer can reproduce
a posting with curl. The movement topic drives ``make ablate`` and Finding 2,
because that finding is *about* the consumer and postings must cross the broker
for loss and duplication to mean anything.

What is emitted is the DOCUMENTED stream -- the transactions before quirks are
applied. The extracts carry the post-quirk legacy view. That asymmetry is the
entire experiment: the shadow books what should have happened, the extract
records what the legacy core did, and the reconciler measures the gap.
"""

from __future__ import annotations

import datetime as dt
import json
import urllib.error
import urllib.request
from collections.abc import Sequence
from dataclasses import dataclass

from legacy_sim.model import Txn

# The suspense account the shadow books a one-sided movement against, so double
# entry holds. Mirrors consumer.SuspenseAccountFor on the Go side.
SUSPENSE_NAMESPACE = "9c4e5b18-27f3-5a6d-b0e4-1d7f8c3a5b92"


def suspense_account_for(currency: str) -> str:
    import uuid

    return str(uuid.uuid5(uuid.UUID(SUSPENSE_NAMESPACE), f"suspense/{currency}"))


@dataclass(frozen=True, slots=True)
class PostResult:
    txn_id: str
    status: int
    posting_id: str
    replayed: bool


class HTTPEmitter:
    """Posts transactions to the ledger's /v1/postings endpoint."""

    def __init__(self, base_url: str, principal: str = "sim", timeout: float = 10.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.principal = principal
        self.timeout = timeout

    def post(self, t: Txn) -> PostResult:
        """Post one transaction against its currency's suspense account.

        The idempotency key is the transaction id, so a replayed day is free:
        the ledger returns the stored response rather than double-booking, which
        is what makes a re-run of ``make demo`` safe.
        """
        body = {
            "kind": "transfer" if t.kind != "reversal" else "reversal",
            "currency": t.currency,
            "business_date": t.business_date.isoformat(),
            "value_date": t.value_date.isoformat(),
            "posted_at": _rfc3339(t.posted_at),
            "reverses_posting_id": None,
            "entries": [
                {"account_id": t.account_id, "amount_minor": t.amount_minor},
                {"account_id": suspense_account_for(t.currency), "amount_minor": -t.amount_minor},
            ],
        }
        payload = json.dumps(body).encode("utf-8")
        req = urllib.request.Request(
            f"{self.base_url}/v1/postings",
            data=payload,
            method="POST",
            headers={
                "Content-Type": "application/json",
                "X-Principal": self.principal,
                "Idempotency-Key": t.txn_id,
            },
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                status = resp.status
                parsed = json.loads(resp.read().decode("utf-8"))
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"ledger rejected {t.txn_id}: {exc.code} {detail}") from exc
        return PostResult(
            txn_id=t.txn_id,
            status=status,
            posting_id=str(parsed.get("posting_id", "")),
            replayed=status == 200,
        )

    def post_many(self, txns: Sequence[Txn]) -> list[PostResult]:
        return [self.post(t) for t in txns]


def movement_payloads(txns: Sequence[Txn]) -> list[dict[str, object]]:
    """Build MovementEvent-shaped dicts for the topic path.

    ``message_id`` is the transaction id: an identity, not a hint. It becomes the
    inbox primary key, and configuration C's exactly-once property is a unique
    violation on it.
    """
    return [
        {
            "message_id": t.txn_id,
            "account_id": t.account_id,
            "amount": {"minor": t.amount_minor, "currency": t.currency, "scale": t.scale},
            "business_date": t.business_date.isoformat(),
            "value_date": t.value_date.isoformat(),
            "posted_at": _rfc3339(t.posted_at),
            "kind": t.kind,
        }
        for t in sorted(txns, key=lambda x: (x.posted_at, x.txn_id))
    ]


def _rfc3339(at: dt.datetime) -> str:
    return at.replace(tzinfo=dt.UTC).isoformat().replace("+00:00", "Z")
