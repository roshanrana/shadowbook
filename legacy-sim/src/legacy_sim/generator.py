"""The deterministic legacy core: accounts, a transaction stream, and EOD.

Determinism (FR-S6, NFR-5) is designed in, not tested for:

* one ``random.Random`` seeded from ``SHADOWBOOK_SEED`` via a documented split,
  so the simulator and the harness never share a stream;
* every collection that reaches an output is sorted by an explicit key;
* every timestamp is derived from the simulated calendar, never ``now()``.
"""

from __future__ import annotations

import datetime as dt
import hashlib
import random
import uuid
from dataclasses import dataclass, field

from legacy_sim.calendar import Calendar, day_count_fraction, round_half_even
from legacy_sim.model import Account, AccountDayBalance, Hold, Txn
from legacy_sim.quirks import Quirks
from legacy_sim.windows import Window

# The documented product catalogue, mirroring internal/ledger/accrual.Products.
# A test asserts the two agree.
PRODUCTS: dict[str, dict[str, int | str]] = {
    "CHK-01": {
        "rate_bp": 0,
        "monthly_fee_minor": 500,
        "min_balance_minor": 100_000,
        "min_balance_fee_minor": 1_200,
        "currency": "USD",
    },
    "SAV-01": {
        "rate_bp": 325,
        "monthly_fee_minor": 0,
        "min_balance_minor": 0,
        "min_balance_fee_minor": 0,
        "currency": "USD",
    },
    "CHK-JPY": {
        "rate_bp": 0,
        "monthly_fee_minor": 300,
        "min_balance_minor": 0,
        "min_balance_fee_minor": 0,
        "currency": "JPY",
    },
}

BASIS_POINT_DENOMINATOR = 10_000

# Namespace for deterministic account and transaction ids. A random uuid4 would
# make every run's extracts differ for no reason (NFR-5).
_NS = uuid.UUID("7d1f4a30-6c25-5e91-8b03-2f9a4d6e1c78")


def seed_for(component: str, base_seed: int) -> int:
    """Split the base seed per component so streams never interfere.

    Documented split: SHA-256 of ``"<component>/<seed>"``, first 8 bytes.
    """
    digest = hashlib.sha256(f"{component}/{base_seed}".encode()).digest()
    return int.from_bytes(digest[:8], "big")


def _det_uuid(*parts: str) -> str:
    return str(uuid.uuid5(_NS, "/".join(parts)))


@dataclass(slots=True)
class DayResult:
    """One simulated business day."""

    business_date: dt.date
    transactions: list[Txn]
    balances: list[AccountDayBalance]


@dataclass(slots=True)
class LegacyCore:
    """A deterministic simulated incumbent core."""

    accounts: list[Account]
    cal: Calendar
    quirks: Quirks
    base_seed: int
    rng: random.Random = field(init=False)
    _ledger: dict[str, int] = field(init=False, default_factory=dict)
    _holds: list[Hold] = field(init=False, default_factory=list)
    _daily_balance_sum: dict[str, int] = field(init=False, default_factory=dict)
    _last_interest_month: tuple[int, int] | None = field(init=False, default=None)

    def __post_init__(self) -> None:
        self.rng = random.Random(seed_for("legacy-sim", self.base_seed))
        self._ledger = {a.account_id: 0 for a in self.accounts}
        self._daily_balance_sum = {a.account_id: 0 for a in self.accounts}

    # -- accounts -------------------------------------------------------------

    @staticmethod
    def build_accounts(base_seed: int, per_product: int = 4) -> list[Account]:
        """Generate the account book deterministically.

        Open dates deliberately straddle 2019-01-01 so Q4's grandfather waiver
        has both sides to act on; without that, Q4 could never produce a break.
        """
        rng = random.Random(seed_for("accounts", base_seed))
        out: list[Account] = []
        for product in sorted(PRODUCTS):
            currency = str(PRODUCTS[product]["currency"])
            for i in range(per_product):
                # Half the accounts pre-date the grandfather cut-off.
                year = 2017 if i % 2 == 0 else 2021
                opened = dt.date(year, 1 + (i * 3) % 12, 1 + (i * 7) % 28)
                out.append(
                    Account(
                        account_id=_det_uuid("account", product, str(i)),
                        product_code=product,
                        currency=currency,
                        opened_on=opened,
                    )
                )
        rng.shuffle(out)
        return sorted(out, key=lambda a: a.account_id)

    # -- the daily stream -----------------------------------------------------

    def _instants_for(self, d: dt.date, n: int) -> list[dt.datetime]:
        """Transaction instants across the day, including the cut-off edge.

        One transaction per day is stamped 16:59:59.999 on purpose: that is the
        exact instant Q2 disagrees about, and a stream that never lands there
        would leave Q2 undetectable.
        """
        out = [dt.datetime.combine(d, dt.time(16, 59, 59, 999_000))]
        for i in range(1, n):
            hour = 9 + (i * 3) % 7
            minute = (i * 17) % 60
            second = (i * 29) % 60
            out.append(dt.datetime.combine(d, dt.time(hour, minute, second)))
        return sorted(out)

    def generate_day(self, d: dt.date) -> list[Txn]:
        """Build one business day's transactions, pre-quirk."""
        txns: list[Txn] = []
        n_per_account = 2
        for a in self.accounts:
            instants = self._instants_for(d, n_per_account)
            for i, at in enumerate(instants):
                magnitude = 1_000 + self.rng.randrange(0, 250_000)
                sign = 1 if self.rng.random() < 0.55 else -1
                counterparty = f"CP-{self.rng.randrange(0, 12):02d}"

                true_hundredths: int | None = None
                if a.currency == "JPY":
                    # The core computes JPY in hundredths of a yen. Roughly half
                    # the amounts land on an exact half-yen, which is where the
                    # documented half-even rounding and Q10's truncation part
                    # company.
                    base = 100 + self.rng.randrange(0, 40_000)
                    fraction = 50 if base % 2 == 1 else 0
                    true_hundredths = sign * (base * 100 + fraction)
                    magnitude = abs(round_half_even(true_hundredths, 100))

                txns.append(
                    Txn(
                        txn_id=_det_uuid("txn", a.account_id, d.isoformat(), str(i)),
                        account_id=a.account_id,
                        currency=a.currency,
                        amount_minor=sign * magnitude,
                        posted_at=at,
                        value_date=d,
                        business_date=self.quirks.business_date_for(at),
                        kind="transfer",
                        counterparty=counterparty,
                        true_hundredths=true_hundredths,
                    )
                )

        # A deliberate same-amount repeat inside 60s, so Q11 has something to
        # suppress, and a reversal pair so Q9 has something to delete.
        if self.accounts:
            a = self.accounts[0]
            base_at = dt.datetime.combine(d, dt.time(11, 0, 0))
            repeat_amount = 7_500
            for i in range(2):
                at = base_at + dt.timedelta(seconds=20 * i)
                txns.append(
                    Txn(
                        txn_id=_det_uuid("dup", a.account_id, d.isoformat(), str(i)),
                        account_id=a.account_id,
                        currency=a.currency,
                        amount_minor=repeat_amount,
                        posted_at=at,
                        value_date=d,
                        business_date=self.quirks.business_date_for(at),
                        kind="transfer",
                        counterparty="CP-DUP",
                    )
                )
            original_id = _det_uuid("rev-orig", a.account_id, d.isoformat())
            rev_at = dt.datetime.combine(d, dt.time(13, 30, 0))
            txns.append(
                Txn(
                    txn_id=original_id,
                    account_id=a.account_id,
                    currency=a.currency,
                    amount_minor=4_242,
                    posted_at=rev_at,
                    value_date=d,
                    business_date=self.quirks.business_date_for(rev_at),
                    kind="transfer",
                    counterparty="CP-REV",
                )
            )
            rev2_at = rev_at + dt.timedelta(minutes=5)
            txns.append(
                Txn(
                    txn_id=_det_uuid("rev", a.account_id, d.isoformat()),
                    account_id=a.account_id,
                    currency=a.currency,
                    amount_minor=-4_242,
                    posted_at=rev2_at,
                    value_date=d,
                    business_date=self.quirks.business_date_for(rev2_at),
                    kind="reversal",
                    counterparty=original_id,
                )
            )
        return sorted(txns, key=lambda t: (t.posted_at, t.txn_id))

    # -- end of day -----------------------------------------------------------

    def _open_hold_total(self, account_id: str, at: dt.datetime) -> int:
        return sum(
            h.amount_minor for h in self._holds if h.account_id == account_id and h.expires_at > at
        )

    def place_hold(
        self, account_id: str, currency: str, amount_minor: int, at: dt.datetime
    ) -> Hold:
        h = Hold(
            hold_id=_det_uuid("hold", account_id, at.isoformat()),
            account_id=account_id,
            currency=currency,
            amount_minor=amount_minor,
            placed_at=at,
            expires_at=self.quirks.hold_expiry(at),
        )
        self._holds.append(h)
        return h

    def run_day(self, d: dt.date) -> DayResult:
        """Apply one business day and return the transactions and EOD balances."""
        raw = self.generate_day(d)
        txns = self.quirks.transform_transactions(raw)
        # Only transactions the legacy core assigned to THIS business date land
        # today; Q2 and Q5 move some of them.
        today = [t for t in txns if t.business_date == d]

        for t in today:
            self._ledger[t.account_id] = self._ledger.get(t.account_id, 0) + t.amount_minor

        # A hold on the first account every day, so Q8 has something to expire.
        if self.accounts:
            a = self.accounts[0]
            self.place_hold(
                a.account_id, a.currency, 5_000, dt.datetime.combine(d, dt.time(10, 0, 0))
            )

        eod_instant = dt.datetime.combine(d, dt.time(23, 59, 59))

        # Interest for the month just ended. The TRIGGER is the first business
        # day of the month; the posting DATE is the core's own rule (Q12).
        if d == self.cal.first_business_day_of_month(d.year, d.month):
            today.extend(self._post_interest(d))

        # Month-end fees.
        if (d + dt.timedelta(days=1)).month != d.month:
            today.extend(self._post_fees(d, eod_instant))

        for a in self.accounts:
            self._daily_balance_sum[a.account_id] += max(self._ledger.get(a.account_id, 0), 0)

        balances = [
            AccountDayBalance(
                account_id=a.account_id,
                currency=a.currency,
                business_date=d,
                ledger_minor=self._ledger.get(a.account_id, 0),
                available_minor=self._ledger.get(a.account_id, 0)
                - self._open_hold_total(a.account_id, eod_instant),
            )
            for a in self.accounts
        ]
        return DayResult(business_date=d, transactions=today, balances=balances)

    def _post_interest(self, on: dt.date) -> list[Txn]:
        """Accrue the previous month's interest.

        ``on`` is the trigger day -- the first business day of the month. The
        posting is DATED by the core's own rule: the calendar first when
        documented, the trigger day under Q12. That date disagreement is the
        whole of Q12, and it only exists because the two can differ.
        """
        dated = self.quirks.interest_post_date(on.year, on.month)
        prev_end = self.cal.first_of_month(on.year, on.month) - dt.timedelta(days=1)
        first = dt.date(prev_end.year, prev_end.month, 1)
        if self._last_interest_month == (first.year, first.month):
            return []
        self._last_interest_month = (first.year, first.month)
        out: list[Txn] = []

        for a in self.accounts:
            p = PRODUCTS[a.product_code]
            rate = int(p["rate_bp"])
            if rate == 0:
                continue
            basis = self.quirks.interest_basis(a.product_code, first)
            _, den = day_count_fraction(first, first + dt.timedelta(days=1), basis)
            total = self._daily_balance_sum.get(a.account_id, 0)
            amount = self.quirks.interest_rounding(total * rate, BASIS_POINT_DENOMINATOR * den)
            if amount:
                self._ledger[a.account_id] = self._ledger.get(a.account_id, 0) + amount
                out.append(
                    Txn(
                        txn_id=_det_uuid("interest", a.account_id, first.isoformat()),
                        account_id=a.account_id,
                        currency=a.currency,
                        amount_minor=amount,
                        posted_at=dt.datetime.combine(dated, dt.time(16, 0, 0)),
                        value_date=dated,
                        business_date=dated,
                        kind="interest",
                        counterparty="INTEREST",
                    )
                )
            self._daily_balance_sum[a.account_id] = 0
        return out

    def _post_fees(self, d: dt.date, at: dt.datetime) -> list[Txn]:
        out: list[Txn] = []
        for a in self.accounts:
            p = PRODUCTS[a.product_code]
            fee = self.quirks.monthly_fee(int(p["monthly_fee_minor"]), a.opened_on)
            threshold = int(p["min_balance_fee_minor"])
            if threshold > 0:
                ledger = self._ledger.get(a.account_id, 0)
                available = ledger - self._open_hold_total(a.account_id, at)
                basis = self.quirks.min_balance_basis(ledger, available)
                if basis < int(p["min_balance_minor"]):
                    fee += threshold
            if fee:
                self._ledger[a.account_id] = self._ledger.get(a.account_id, 0) - fee
                out.append(
                    Txn(
                        txn_id=_det_uuid("fee", a.account_id, d.isoformat()),
                        account_id=a.account_id,
                        currency=a.currency,
                        amount_minor=-fee,
                        posted_at=dt.datetime.combine(d, dt.time(16, 30, 0)),
                        value_date=d,
                        business_date=d,
                        kind="fee",
                        counterparty="FEE",
                    )
                )
        return out

    # -- windows --------------------------------------------------------------

    def run_window(self, w: Window) -> list[DayResult]:
        """Run every business day of a window, in order."""
        return [self.run_day(d) for d in w.business_days(self.cal)]
