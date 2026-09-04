# Domain model — the rules a ledger has to get exactly right

This is the part of SHADOWBOOK that is domain work rather than systems work: how
money, dates, balances and identity are represented, and *why each choice is the
way it is*. Most of these decisions are only interesting because the wrong
version of them is plausible, ships, and is discovered years later by a
reconciliation break.

Every rule below is enforced somewhere a reviewer can point at — a type, a
database constraint, or a test — not by convention.

---

## 1. Money is an integer, and its scale is not the caller's business

```go
type Amount struct {
    Minor    int64          // 1234 == $12.34
    Currency Currency       // "USD"
    Scale    uint8          // 2 for USD, 0 for JPY -- from the registry
}
```

Three separate decisions:

**Integer minor units, never floating point.** `0.1 + 0.2 != 0.3` is the usual
reason given; the better one is that a float silently *rounds*, so an error
becomes invisible at the moment it is created rather than at the moment it is
detected.

**Scale comes from a compile-time registry, not from the caller.** `New` looks
up the currency's scale and populates the field. A caller cannot construct a JPY
amount with scale 2, because that is exactly the bug being modelled — quirk Q10
seeds a legacy core that stores JPY with two decimals and truncates toward zero.
If the API let a caller pass a scale, the twin could reproduce the legacy bug by
accident and the reconciler would find nothing.

**`math.MinInt64` is rejected at construction.** It has no negation in two's
complement, so allowing it would make `Neg()` a partial function on a type whose
whole job is arithmetic. Rejecting one value at the boundary buys totality
everywhere else.

> Enforced by: `internal/money`, 100% statement coverage. `ErrUnknownCurrency`
> for anything absent from the registry.

---

## 2. Business date is not wall-clock date

A payment made at 17:30 on Tuesday belongs to Wednesday's business date. This is
not a rounding convention — it changes which day interest accrues on, which
statement a transaction appears in, and whether a fee is assessed.

**The cut-off is 17:00:00.000, exclusive.** A transaction stamped exactly
17:00:00.000 rolls to the next business day. Quirk Q2 seeds a legacy core that
treats the boundary as *inclusive*, which moves exactly the transactions landing
on that millisecond, and nothing else. It is the smallest possible divergence
that still moves money between days, which is why it is worth seeding: a twin
that gets this wrong looks correct on almost every transaction.

**Business days come from a named calendar** (`USFederal`), not from
"weekday and not in a hard-coded list". Quirk Q5 seeds a core that treats
Columbus Day as a business day. Both sides run the *same* calendar type with
different data, so the difference is in the data and can be diffed.

> The Go and Python calendars are asserted to agree over 1,096 consecutive days
> by a golden test — two independent implementations of a calendar will diverge
> eventually, and the only question is whether you find out from a test or from
> a customer.

---

## 3. Day-count conventions return a fraction, not a float

```go
func DayCountFraction(from, to BusinessDate, b Basis) (num, den int)
```

Interest for a period is `principal × rate × days / basis`. Returning
`float64(days)/float64(basis)` would round *before* the multiply, so the error
scales with the principal. Returning the numerator and denominator separately
keeps the whole calculation in integers until a single, explicit rounding step
at the end.

Three bases exist for a reason:

| Basis | Role |
|---|---|
| `ACT/365` | the documented shadow basis for all products in a normal year |
| `ACT/ACT` | the documented shadow basis in a leap year (366 in the denominator) |
| `ACT/360` | **not** a shadow basis — it exists so quirk Q3 can seed a legacy core that quietly uses it on one product |

`ACT/360` pays roughly 1.4% more interest than `ACT/365` on the same balance.
On a small balance that difference is smaller than one minor unit, which is why
it survives in real systems for years.

---

## 4. Rounding is a named policy, not a default

`RoundHalfEven` (banker's rounding) is the documented policy; `RoundHalfUp`
exists so quirk Q1 can seed a core that uses it instead. Half-even is chosen for
the usual reason — half-up is biased upward, and over millions of postings the
bias accumulates in one direction.

**This is where the honest limitation lives.** At small balances, a day-count
basis difference (Q3) and a rounding-policy difference (Q1) both present as
"off by one minor unit". No amount of additional classification rules can
separate them, because the information needed is not in the delta. The report
states this rather than hiding it; see `reports/FINDINGS.md`, Finding 1a.

---

## 5. Balances are derived, not stored

There is no `balance` column. A balance is:

```sql
SELECT coalesce(c.balance_minor, 0) + coalesce(sum(e.amount_minor), 0)
FROM       (SELECT 1) AS anchor
LEFT JOIN LATERAL (SELECT balance_minor, last_entry_id FROM checkpoints
                   WHERE account_id = $1 AND business_date <= $2
                   ORDER BY business_date DESC LIMIT 1) c ON true
LEFT JOIN  entries e ON e.account_id = $1
                    AND e.entry_id > coalesce(c.last_entry_id, 0)
                    AND e.business_date <= $2
GROUP BY c.balance_minor
```

Checkpoints bound the scan; the entries are the truth. A stored balance is a
cache that can silently disagree with the entries that produced it, and
reconciling a cache against its own source is a category of bug this design
simply does not have.

The `LEFT JOIN LATERAL` and both `coalesce`s are load-bearing: an earlier
version returned **no row at all** for an account with no checkpoint yet, so a
brand-new account read as "balance unknown" rather than zero. That was caught by
executing the schema against PostgreSQL 16 *before* the design was approved
(D-015).

**Three balances, not one:** ledger (all posted entries), available (ledger
minus active holds), and pending. Quirk Q7 seeds a core that assesses a
minimum-balance fee against the *ledger* balance where the documented behaviour
is *available* — a difference that only appears on accounts with an active hold.

---

## 6. Identity is a database constraint, not application logic

Idempotency is not "check whether we've seen this key, then insert". That is a
race, and under concurrency it is a race you lose quietly.

```
INSERT INTO idempotency_keys ...   -- claim first
  └─ 23505 on the primary key  →  this is a replay; return the original effect
```

The unique violation *is* the mechanism. The same pattern appears three times:

| Table | Protects against |
|---|---|
| `idempotency_keys` | the same client request submitted twice |
| `inbox` | the same broker message delivered twice (consumer modes C and D) |
| `postings` | a posting id derived deterministically from (principal, key) |

> Verified by `TestIdempotencyRace64`: 64 goroutines released simultaneously
> with the same idempotency key produce **one** posting, two entries, one outbox
> row, and 64 identical responses.

A replay with a *different* body is not idempotent — it is a client bug — so the
request body is hashed and a mismatch returns 409 rather than silently returning
someone else's result. The hash deliberately excludes the key itself: the hash
answers "is this the same body", and the key is the question, not the answer.

---

## 7. The invariants live in the database

Three rules are enforced by PostgreSQL, so no code path can bypass them:

```sql
-- entries are append-only
CREATE TRIGGER ... BEFORE UPDATE OR DELETE ON entries
    EXECUTE FUNCTION deny_mutation();

-- every posting sums to zero, checked at COMMIT
CREATE CONSTRAINT TRIGGER entries_zero_sum AFTER INSERT ON entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_posting_zero_sum();
```

The zero-sum trigger is **deferred** because entries arrive one row at a time; a
non-deferred check would fail on the first row of every balanced posting. It
fires at `COMMIT`, which is the only moment the question is meaningful.

The global invariant — `SUM(amount_minor) GROUP BY currency = 0` across the
entire ledger — is checked on a timer and exported as a Prometheus gauge, so a
violation is visible in seconds rather than at month end.

**And the invariant is not sufficient**, which is the point of Finding 2. Every
delivery configuration held the zero-sum rule in every run, including the ones
that duplicated thousands of movements. A duplicated posting is perfectly
balanced and completely wrong. Correctness at the row level does not imply
correctness at the ledger level, and only a second source of truth —
reconciliation — can tell the difference.

---

## 8. Determinism is a requirement, not a nicety

Same seed in, byte-identical extracts out. Asserted by test.

This sounds like tidiness and is actually the load-bearing property of Finding 1:
each quirk is measured *in isolation* against a control run with every quirk
disabled. If enabling a quirk perturbed anything else, the per-quirk numbers
would not be controlled experiments.

An early version used a single sequential RNG, so enabling the quirk that adds a
business day (Q5) shifted every subsequent draw — the runs were not controlled at
all, and the numbers looked entirely reasonable. Transaction values are now a
hash of `(seed, account, date, index)`, so adding or removing a day changes only
that day.

---

## Where each rule is exercised

| Rule | Code | Test |
|---|---|---|
| minor units, registry scale | `internal/money` | 100% coverage; `ErrUnknownCurrency`, `MinInt64` rejection |
| cut-off exclusive | `internal/bizdate.CutOff` | Q2 detection |
| day-count as a fraction | `internal/bizdate.DayCountFraction` | Q3, Q6 detection |
| half-even rounding | `internal/bizdate.RoundHalfEven` | Q1 detection |
| calendar agreement | `internal/bizdate` + `legacy_sim.calendar` | 1,096-day golden test |
| derived balances | `store.LedgerBalance` | `internal/ledger/balance` |
| idempotency as a constraint | `posting.ClaimIdempotencyKey` | `TestIdempotencyRace64` |
| append-only, zero-sum | `migrations/0003_invariants.sql` | `TestAppendOnly`, `TestZeroSumEnforcedAtCommit` |
| global invariant | `obs.Checker` | `TestAllModesPreserveTheGlobalInvariant` |
| determinism | `legacy_sim.generator` | `test_two_runs_of_the_same_seed_are_byte_identical` |
