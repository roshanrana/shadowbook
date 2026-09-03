# T-008 — Ledger migrations 0001–0006 with the DDL invariants
Status: todo      Milestone: M0   Wave: 3
Depends on: T-002

## Goal
The ledger schema exists as goose plain-SQL migrations, embedded for offline application, with the three invariants enforced in the database: append-only entries, deferred zero-sum per posting, and idempotency as a primary key.

## Context
- **The SQL is already written and already verified.** LLD §3.3 holds it; LLD §8 records that it was executed against PostgreSQL 16.13 and its invariants exercised. Copy it; do not improve it.
- Two defects were already found and fixed during verification (`BIGGSERIAL`, and a balance query missing `GROUP BY` that returned no row for an account with no checkpoint). If you find yourself rewriting either, re-read LLD §8 first.
- D-014: **forward-only**. No down migrations. Do not add them "for safety".
- LLD §5: numbered `NNNN_slug.sql`, applied by goose from `embed.FS` at start-up (NFR-8).
- CLAUDE.md: "Idempotency is a database constraint, not application logic."

## Contracts to honor
LLD §3.3 verbatim: eleven tables (`accounts`, `postings`, `entries`, `idempotency_keys`, `holds`, `checkpoints`, `outbox`, `inbox` here; the recon three belong to T-035), the `deny_mutation` and `assert_posting_zero_sum` functions, and the triggers that use them.

## File scope
Create: `migrations/0001_accounts.sql`, `0002_postings_entries.sql`, `0003_invariants.sql`, `0004_idempotency.sql`, `0005_balances_holds.sql`, `0006_outbox_inbox.sql`, `migrations/embed.go`, `migrations/migrations_test.go`
Modify: —

## Suggested steps
1. Split LLD §3.3 across the six files so each is independently readable; `0003_invariants.sql` holds both functions and all triggers.
2. `embed.go` exposes `//go:embed *.sql` as an `embed.FS`.
3. Write an integration test using testcontainers that applies all migrations to an empty Postgres 16 and then asserts each invariant behaves — these six assertions are already proven to hold, so a failure means the SQL was transcribed wrongly:
   - unbalanced posting rejected at COMMIT with `sums to … need 0`
   - single-entry posting rejected with `has 1 entries, need >= 2`
   - `UPDATE` on `entries` raises `entries is append-only`
   - `DELETE` on `entries` raises the same
   - duplicate `(principal, idem_key)` raises 23505 on `idempotency_keys_pkey`
   - duplicate `message_id` raises 23505 on `inbox_pkey`
4. Assert `SUM(amount_minor) GROUP BY currency = 0` after the rejections.

## Acceptance criteria
- [ ] All six migrations apply clean to an empty Postgres 16 in one goose run
- [ ] Re-running goose is a no-op
- [ ] All six invariant assertions above pass as integration tests
- [ ] The balance query from LLD §3.3 returns one row for: checkpoint + entries, entries with no checkpoint, and neither (value `0`)
- [ ] No down-migration file exists
- [ ] `grep -rn "BIGGSERIAL" migrations/` returns nothing

## Validation
```
go test -race ./migrations/...
make check
```

## Out of scope
The recon database schema (T-035). Any Go query code (T-009). Seed or fixture data — D-014 and FR-S6 forbid it; accounts come from `legacy-sim`.

## Handoff notes
_(filled by the worker)_
