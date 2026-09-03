---
name: ledger-invariants
description: Rules and test patterns for any code that touches the SHADOWBOOK ledger posting path — double-entry, append-only, derived balances, idempotency as a DB constraint, money as integer minor units, explicit business dates. Read this whenever a task file scope includes the ledger service, its schema, its consumer, or its outbox.
---

# Ledger invariants

These are properties, not preferences. A change that can violate one is wrong even if its tests pass.

## The five invariants

1. **Zero-sum postings.** Every posting has ≥ 2 entries; `SUM(amount_minor)` over a posting's entries is exactly 0 in the posting currency. Enforce in the DB (trigger or deferred constraint) *and* in code.
2. **Append-only entries.** No UPDATE or DELETE on the entries table, ever. Enforce with a DB rule/trigger that raises. Reversals insert contra entries carrying `reverses_entry_id`.
3. **Derived balance.** `balance(account, t) = checkpoint_balance + SUM(entries after checkpoint up to t)`. No column is the balance. Checkpoints are inserts, never updates.
4. **Idempotency by constraint.** `idempotency_keys(principal, key)` is UNIQUE; the row is written in the same transaction as the entries; a duplicate is detected by the constraint violation, not by a prior SELECT. Same key + different body hash → distinct error (`IdempotencyBodyMismatch`).
5. **Global zero.** `SUM(amount_minor) over all entries GROUP BY currency = 0` at all times. Exposed as a metric (`shadowbook_ledger_invariant_ok`) checked on a ticker; asserted after every integration scenario.

## Money

- Integer minor units + ISO currency + scale. JPY scale 0. Never float, never string arithmetic.
- One rounding function per rule, named (`RoundHalfEven`, `RoundHalfUp`), tested at the .5 boundaries and on negative values.
- Allocation (splitting X three ways) must sum to X; the remainder goes to a deterministic leg.

## Dates

- `business_date`, `value_date`, `posted_at` are distinct fields. Cut-off is a config value whose name carries its semantic (`CutoffExclusive`).
- Never call `time.Now()` in domain code; inject a clock.

## Consumer delivery modes

Implement as a strategy behind one interface so the ablation can swap them:

| Mode | Commit | Dedupe |
|---|---|---|
| `at-most-once` | before apply | none |
| `at-least-once` | after apply | none |
| `inbox` | after apply, inbox row in same tx | `inbox(message_id)` UNIQUE |
| `transactional` | Kafka tx incl. offsets | producer idempotence |

The mode is logged at startup and stamped on every run artefact.

## Required test patterns

- Table-driven tests for every rounding function and every cut-off boundary (both sides, ±1 ms).
- A property test: random valid postings → global zero holds; random reversals → still holds.
- An idempotency test that races N goroutines on one key and asserts exactly one effect (`-race` on).
- One integration scenario per ablation mode that kills the broker container mid-stream and asserts the expected loss/duplicate profile.
- Every named adversarial scenario in the README maps to a test function by name.

## Go specifics

- `ctx` first parameter everywhere; `ctx.Done()` in every blocking select; `errgroup.WithContext` for fan-out; `signal.NotifyContext` for shutdown.
- No goroutine without an owner that can cancel it. `goleak` in tests.
- `GOMEMLIMIT` set in compose slightly below the container limit.
- Wrap errors with `%w`; sentinel errors follow the taxonomy in `03-lld.md`.
