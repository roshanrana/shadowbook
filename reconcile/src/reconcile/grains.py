"""The three comparison grains (FR-R1).

Each grain sees something the others cannot, which is the whole reason there are
three. Q9 (a reversal that deletes its original) is invisible at the control
total by design -- the deleted pair nets to zero -- and only the transaction
grain catches it. Q12 (interest on the wrong date) is a movement on the wrong
day, which the account-day grain sees as two breaks that the transaction grain
reports as a timing pair.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence

from reconcile.classify import classify_matched, classify_one_sided, explain_whole_movement
from reconcile.model import Break, Classification, Grain, Movement


def transaction_grain(
    legacy: Sequence[Movement],
    shadow: Sequence[Movement],
    legacy_window: Sequence[Movement] | None = None,
    shadow_window: Sequence[Movement] | None = None,
    fee_amounts: frozenset[int] = frozenset(),
) -> list[Break]:
    """Compare movement by movement, keyed on transaction id.

    The *_window arguments carry the whole window so a one-sided record can be
    matched against its counterpart on another date and classified as timing
    rather than as a defect.
    """
    legacy_window = legacy_window if legacy_window is not None else legacy
    shadow_window = shadow_window if shadow_window is not None else shadow
    by_legacy = {m.txn_id: m for m in legacy}
    by_shadow = {m.txn_id: m for m in shadow}
    breaks: list[Break] = []

    for txn_id in sorted(set(by_legacy) | set(by_shadow)):
        left, right = by_legacy.get(txn_id), by_shadow.get(txn_id)

        if left is not None and right is not None:
            if (
                left.amount_minor == right.amount_minor
                and left.business_date == right.business_date
            ):
                continue
            if left.business_date != right.business_date:
                offset = (right.business_date - left.business_date).days
                breaks.append(
                    Break(
                        grain=Grain.TRANSACTION,
                        business_date=left.business_date,
                        currency=left.currency,
                        account_id=left.account_id,
                        txn_id=txn_id,
                        legacy_minor=left.amount_minor,
                        shadow_minor=right.amount_minor,
                        delta_minor=left.amount_minor - right.amount_minor,
                        classification=Classification.TIMING,
                        signature=f"timing:{offset:+d}d",
                    )
                )
                continue
            classification, signature = classify_matched(
                left.amount_minor, right.amount_minor, fee_amounts
            )
            breaks.append(
                Break(
                    grain=Grain.TRANSACTION,
                    business_date=left.business_date,
                    currency=left.currency,
                    account_id=left.account_id,
                    txn_id=txn_id,
                    legacy_minor=left.amount_minor,
                    shadow_minor=right.amount_minor,
                    delta_minor=left.amount_minor - right.amount_minor,
                    classification=classification,
                    signature=signature,
                )
            )
            continue

        present = left or right
        assert present is not None
        side = "legacy" if left is not None else "shadow"
        other = shadow_window if left is not None else legacy_window
        # A record absent from this day but present on the other side elsewhere
        # in the window is a timing difference, not a missing record.
        by_id = {m.txn_id for m in other}
        if present.txn_id in by_id:
            counterpart = next(m for m in other if m.txn_id == present.txn_id)
            offset = (counterpart.business_date - present.business_date).days
            breaks.append(
                Break(
                    grain=Grain.TRANSACTION,
                    business_date=present.business_date,
                    currency=present.currency,
                    account_id=present.account_id,
                    txn_id=txn_id,
                    legacy_minor=present.amount_minor
                    if left is not None
                    else counterpart.amount_minor,
                    shadow_minor=counterpart.amount_minor
                    if left is not None
                    else present.amount_minor,
                    delta_minor=0,
                    classification=Classification.TIMING,
                    signature=f"timing:{offset:+d}d",
                )
            )
            continue
        classification, signature = classify_one_sided(side, present, other)
        breaks.append(
            Break(
                grain=Grain.TRANSACTION,
                business_date=present.business_date,
                currency=present.currency,
                account_id=present.account_id,
                txn_id=txn_id,
                legacy_minor=present.amount_minor if left is not None else None,
                shadow_minor=present.amount_minor if right is not None else None,
                delta_minor=present.amount_minor if left is not None else -present.amount_minor,
                classification=classification,
                signature=signature,
            )
        )
    return breaks


def account_day_grain(
    legacy: Sequence[Movement],
    shadow: Sequence[Movement],
    business_date: dt.date,
    fee_amounts: frozenset[int] = frozenset(),
) -> list[Break]:
    """Compare each account's NET MOVEMENT for a business date.

    Net movement, not cumulative balance: a movement booked on the wrong day
    shows up as two breaks, one on each date, which is what makes a timing
    difference visible and ageable.
    """

    def totals(ms: Sequence[Movement]) -> dict[tuple[str, str], int]:
        out: dict[tuple[str, str], int] = {}
        for m in ms:
            if m.business_date != business_date:
                continue
            key = (m.account_id, m.currency)
            out[key] = out.get(key, 0) + m.amount_minor
        return out

    left, right = totals(legacy), totals(shadow)
    breaks: list[Break] = []
    for account_id, currency in sorted(set(left) | set(right)):
        lv = left.get((account_id, currency), 0)
        rv = right.get((account_id, currency), 0)
        if lv == rv:
            continue
        if lv == 0 or rv == 0:
            # One side booked nothing at all for this account-day.
            classification = Classification.TIMING
            signature = f"account_day_missing:{'shadow' if rv == 0 else 'legacy'}"
        else:
            classification, signature = classify_matched(lv, rv, fee_amounts)
            if signature.startswith("unexplained:"):
                whole = explain_whole_movement(
                    lv - rv,
                    [m for m in shadow if m.business_date == business_date],
                    account_id,
                )
                if whole is not None:
                    classification, signature = Classification.MODEL_DIFFERENCE, whole
        breaks.append(
            Break(
                grain=Grain.ACCOUNT_DAY,
                business_date=business_date,
                currency=currency,
                account_id=account_id,
                legacy_minor=lv,
                shadow_minor=rv,
                delta_minor=lv - rv,
                classification=classification,
                signature=signature,
            )
        )
    return breaks


def control_total_grain(
    legacy: Sequence[Movement],
    shadow: Sequence[Movement],
    business_date: dt.date,
    fee_amounts: frozenset[int] = frozenset(),
) -> list[Break]:
    """Compare the book-level total per currency for a business date."""

    def totals(ms: Sequence[Movement]) -> dict[str, int]:
        out: dict[str, int] = {}
        for m in ms:
            if m.business_date != business_date:
                continue
            out[m.currency] = out.get(m.currency, 0) + m.amount_minor
        return out

    left, right = totals(legacy), totals(shadow)
    breaks: list[Break] = []
    for currency in sorted(set(left) | set(right)):
        lv, rv = left.get(currency, 0), right.get(currency, 0)
        if lv == rv:
            continue
        classification, signature = classify_matched(lv, rv)
        breaks.append(
            Break(
                grain=Grain.CONTROL_TOTAL,
                business_date=business_date,
                currency=currency,
                legacy_minor=lv,
                shadow_minor=rv,
                delta_minor=lv - rv,
                classification=classification,
                signature=signature,
            )
        )
    return breaks


def balance_grain(
    legacy_balances: dict[tuple[str, str], tuple[int, int]],
    shadow_balances: dict[tuple[str, str], tuple[int, int]],
    business_date: dt.date,
    fee_amounts: frozenset[int] = frozenset(),
) -> list[Break]:
    """Compare closing LEDGER and AVAILABLE balances from the BAL extracts.

    Available balance is the only place a hold is visible: holds never touch the
    ledger balance. Q8 (holds expiring at midnight on placement + 3 instead of
    72 hours later) therefore cannot be seen at any movement grain at all -- it
    exists only here. Reconciling movements alone would report Q8 as undetected
    and the reason would not be the reconciliation's fault.
    """
    breaks: list[Break] = []
    for account_id, currency in sorted(set(legacy_balances) | set(shadow_balances)):
        lledger, lavail = legacy_balances.get((account_id, currency), (0, 0))
        sledger, savail = shadow_balances.get((account_id, currency), (0, 0))

        if lledger != sledger:
            classification, signature = classify_matched(lledger, sledger, fee_amounts)
            breaks.append(
                Break(
                    grain=Grain.ACCOUNT_DAY,
                    business_date=business_date,
                    currency=currency,
                    account_id=account_id,
                    txn_id="balance:ledger",
                    legacy_minor=lledger,
                    shadow_minor=sledger,
                    delta_minor=lledger - sledger,
                    classification=classification,
                    signature=f"balance:ledger:{signature}",
                )
            )
        if lavail != savail and (lledger - lavail) != (sledger - savail):
            # The HOLD component differs, not just the ledger underneath it.
            breaks.append(
                Break(
                    grain=Grain.ACCOUNT_DAY,
                    business_date=business_date,
                    currency=currency,
                    account_id=account_id,
                    txn_id="balance:available",
                    legacy_minor=lavail,
                    shadow_minor=savail,
                    delta_minor=lavail - savail,
                    classification=Classification.MODEL_DIFFERENCE,
                    signature="hold:expiry",
                )
            )
    return breaks


def all_grains(
    legacy: Sequence[Movement],
    shadow: Sequence[Movement],
    business_date: dt.date,
    legacy_window: Sequence[Movement] | None = None,
    shadow_window: Sequence[Movement] | None = None,
    fee_amounts: frozenset[int] = frozenset(),
) -> list[Break]:
    """Run all three movement grains for one business date.

    ``legacy_window`` and ``shadow_window`` are the WHOLE window's movements.
    The transaction grain needs them: a transaction the legacy core moved to a
    different business date is a TIMING difference, and it can only be
    recognised as one by looking outside the day being reconciled. Filtering
    both sides to a single date first -- which an earlier version of this
    function did -- turns every timing difference into a pair of one-sided
    breaks classified as defects.
    """
    lw = list(legacy_window) if legacy_window is not None else list(legacy)
    sw = list(shadow_window) if shadow_window is not None else list(shadow)
    return [
        *transaction_grain(
            [m for m in legacy if m.business_date == business_date],
            [m for m in shadow if m.business_date == business_date],
            lw,
            sw,
            fee_amounts,
        ),
        *account_day_grain(legacy, shadow, business_date, fee_amounts),
        *control_total_grain(legacy, shadow, business_date, fee_amounts),
    ]
