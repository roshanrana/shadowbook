"""Extract ingest, including the shapes that arrive wrong (FR-R4)."""

from __future__ import annotations

import datetime as dt

import pytest

from legacy_sim.extracts import render_txn_extract, truncate_extract
from legacy_sim.model import Txn
from reconcile.ingest import ExtractFormatError, ExtractInbox, parse, parse_balances
from reconcile.model import IngestStatus


def txn(i: int, minor: int = 1000, currency: str = "USD") -> Txn:
    at = dt.datetime(2028, 2, 29, 9 + i, 0, 0)
    return Txn(
        txn_id=f"t{i}",
        account_id="acct-1",
        currency=currency,
        amount_minor=minor,
        posted_at=at,
        value_date=at.date(),
        business_date=at.date(),
        kind="transfer",
    )


def body(n: int = 3) -> str:
    return render_txn_extract([txn(i) for i in range(n)], dt.date(2028, 2, 29), 1)


def test_a_good_extract_is_accepted() -> None:
    r = parse(body(3))
    assert r.status is IngestStatus.ACCEPTED
    assert r.record_count == r.declared_count == 3
    assert r.control_total == r.declared_control_total == {"USD": 3000}
    assert [m.txn_id for m in r.movements] == ["t0", "t1", "t2"]


def test_a_truncated_extract_is_recorded_not_raised() -> None:
    """The named scenario. A short file must produce a break, not an exception --
    a reconciler that dies on a bad file reports nothing at all."""
    r = parse(truncate_extract(body(10), keep_fraction=0.5))
    assert r.status is IngestStatus.TRUNCATED
    assert r.record_count < r.declared_count
    assert r.movements, "the rows that did arrive are still usable"


def test_a_trailer_that_lies_about_the_total_is_caught() -> None:
    b = body(3).replace("TRL|3|USD:3000", "TRL|3|USD:9999")
    r = parse(b)
    assert r.status is IngestStatus.TRAILER_MISMATCH
    assert r.control_total == {"USD": 3000}
    assert r.declared_control_total == {"USD": 9999}


def test_a_file_with_no_trailer_at_all_is_truncated() -> None:
    lines = body(3).rstrip("\n").split("\n")
    r = parse("\n".join(lines[:-1]) + "\n")
    assert r.status is IngestStatus.TRUNCATED
    assert r.declared_count == -1


def test_a_byte_identical_redelivery_is_not_double_counted() -> None:
    inbox = ExtractInbox()
    first = inbox.offer(parse(body(3)), received_on=dt.date(2028, 2, 29))
    second = inbox.offer(parse(body(3)), received_on=dt.date(2028, 2, 29))
    assert first.status is IngestStatus.ACCEPTED
    assert second.status is IngestStatus.DUPLICATE
    assert inbox.seen() == 1


def test_a_corrected_redelivery_is_compared_not_discarded() -> None:
    """A core re-sending a corrected file is normal. Only a byte-identical
    repeat is a duplicate."""
    inbox = ExtractInbox()
    inbox.offer(parse(body(3)), received_on=dt.date(2028, 2, 29))
    corrected = inbox.offer(parse(body(4)), received_on=dt.date(2028, 2, 29))
    assert corrected.status is not IngestStatus.DUPLICATE


def test_a_late_extract_is_flagged_but_still_used() -> None:
    inbox = ExtractInbox()
    r = inbox.offer(parse(body(3)), received_on=dt.date(2028, 3, 5))
    assert r.status is IngestStatus.LATE
    assert r.movements, "a late file is still reconciled"


def test_a_non_extract_is_rejected_outright() -> None:
    for bad in ("", "not an extract\n", "HDR|WRONGMAGIC|TXN|20280229|001|1\nTRL|0|\n"):
        with pytest.raises(ExtractFormatError):
            parse(bad)


def test_multi_currency_control_totals_round_trip() -> None:
    b = render_txn_extract(
        [txn(0, 1000, "USD"), txn(1, -250, "USD"), txn(2, 700, "JPY")],
        dt.date(2028, 2, 29),
        1,
    )
    r = parse(b)
    assert r.status is IngestStatus.ACCEPTED
    assert r.control_total == {"USD": 750, "JPY": 700}


def test_bal_extract_parses_ledger_and_available() -> None:
    b = "HDR|SHADOWBOOK|BAL|20280229|001|1\nDTL|acct-1|USD|5000|4000\nTRL|1|USD:5000\n"
    assert parse_balances(b) == {("acct-1", "USD"): (5000, 4000)}
