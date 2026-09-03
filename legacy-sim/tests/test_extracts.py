"""The extract wire format (FR-S3) and the adversarial ingest shapes."""

from __future__ import annotations

import datetime as dt

from legacy_sim.extracts import (
    render_bal_extract,
    render_txn_extract,
    truncate_extract,
)
from legacy_sim.model import AccountDayBalance, Txn


def txn(txn_id: str, currency: str, minor: int, hour: int = 10) -> Txn:
    at = dt.datetime(2028, 2, 29, hour, 0, 0)
    return Txn(
        txn_id=txn_id,
        account_id="acct-1",
        currency=currency,
        amount_minor=minor,
        posted_at=at,
        value_date=at.date(),
        business_date=at.date(),
        kind="transfer",
    )


def test_header_detail_trailer_shape() -> None:
    body = render_txn_extract([txn("t1", "USD", 1000)], dt.date(2028, 2, 29), 42)
    lines = body.rstrip("\n").split("\n")
    assert lines[0] == "HDR|SHADOWBOOK|TXN|20280229|001|42"
    assert lines[1].startswith("DTL|t1|acct-1|USD|1000|2|")
    assert lines[-1] == "TRL|1|USD:1000"
    assert body.endswith("\n"), "a trailing newline is part of the format"


def test_control_total_is_per_currency_and_signed() -> None:
    body = render_txn_extract(
        [txn("a", "USD", 1000), txn("b", "USD", -250, 11), txn("c", "JPY", 700, 12)],
        dt.date(2028, 2, 29),
        1,
    )
    assert body.rstrip("\n").split("\n")[-1] == "TRL|3|JPY:700;USD:750"


def test_rows_are_sorted_so_bytes_are_deterministic() -> None:
    a = render_txn_extract(
        [txn("z", "USD", 1, 12), txn("a", "USD", 2, 10)], dt.date(2028, 2, 29), 1
    )
    b = render_txn_extract(
        [txn("a", "USD", 2, 10), txn("z", "USD", 1, 12)], dt.date(2028, 2, 29), 1
    )
    assert a == b


def test_bal_extract_carries_both_balances() -> None:
    body = render_bal_extract(
        [AccountDayBalance("acct-1", "USD", dt.date(2028, 2, 29), 5000, 4000)],
        dt.date(2028, 2, 29),
        1,
    )
    lines = body.rstrip("\n").split("\n")
    assert lines[0].startswith("HDR|SHADOWBOOK|BAL|")
    assert lines[1] == "DTL|acct-1|USD|5000|4000"
    assert lines[-1] == "TRL|1|USD:5000"


def test_truncation_leaves_the_trailer_lying() -> None:
    """The named scenario: truncated extract with a bad trailer.

    The trailer is deliberately not recomputed. A truncated file whose trailer
    agreed with its body would be indistinguishable from a quiet day, and the
    reconciler would have nothing to detect.
    """
    full = render_txn_extract(
        [txn(f"t{i}", "USD", 100 + i, 9 + i) for i in range(10)], dt.date(2028, 2, 29), 1
    )
    cut = truncate_extract(full, keep_fraction=0.5)
    assert cut.rstrip("\n").split("\n")[-1] == full.rstrip("\n").split("\n")[-1]
    assert cut.count("DTL") < full.count("DTL")
    claimed = int(cut.rstrip("\n").split("\n")[-1].split("|")[1])
    assert claimed != cut.count("DTL"), "the trailer must disagree with the body"
