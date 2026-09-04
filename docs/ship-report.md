# SHADOWBOOK — ship report

Evidence of readiness, and an honest account of what is not ready. Produced at
the Phase 7 gate; the go/no-go is the owner's.

**Recommendation: ship as a portfolio artefact. Do not describe Finding 2 as
measured.** Finding 1 is complete and reproducible. Finding 2's mechanism is
demonstrated and its numbers are not.

## 1. What runs

| Capability | State | Evidence |
|---|---|---|
| `make check` | green, 30s | format, lint, `mypy --strict`, `go vet`, unit, integration, `-race`, plus the generated-code and calendar-golden gates |
| `make demo` | green, ~2s | 12 of 12 quirks detected across both windows; regenerates `reports/FINDINGS.md` |
| `make coverage` | green | ledger 89.9% (target 85%), posting path 96.3% (target 95%), reconcile 97%, legacy-sim ≥85% |
| `make perf` | green at the smoke rate | p50 2.3ms, p95 3.1ms, p99 3.7ms at 200 postings/s |
| `make report` | green | deterministic; renders Finding 2 as **not run** rather than implying numbers |
| `go run ./cmd/ledger` | green | migrations apply at start-up, `/readyz` reports the live invariant, `/metrics` serves |
| `make ablate` | **refuses to run** | no Docker daemon here; see §4 |
| `make security` | **fails on two checks** | both are recorded deviations; see §5 |

## 2. Findings

**Finding 1 — complete.** All twelve seeded quirks are detected. Each is measured
in isolation against a control run with every quirk disabled, which is the only
way "business days until this surfaced" means what it says. A combined run with
all twelve enabled is reported alongside and detects **eleven** — quirks
compound, and that gap is itself a result.

**Finding 2 — mechanism demonstrated, numbers not measured.** The consumer test
suite shows deterministically that mode B duplicates under redelivery (4 entries
for 2 movements), C and D suppress it via `inbox_pkey` (2 entries), and A loses
a batch outright because it commits offsets before applying. All four preserve
the global invariant, which is the point: duplication is a correctness failure
the zero-sum rule cannot catch. Loss, duplication and latency **numbers** under
real broker chaos require the three-broker profile and have not been produced.

## 3. Requirements not met

| ID | Target | Actual | Assessment |
|---|---|---|---|
| NFR-1 | ≥ 2,000 postings/s | ~1,584/s saturated in this environment | **Not verified.** Unmeasured on the target machine. Every request succeeded and the invariant held throughout, so this is capacity, not correctness |
| NFR-2 | p99 ≤ 50ms | 3.7ms at 200/s; 164ms at 1,000/s; 255ms at 2,000/s offered | **Met at low rate, not at the NFR-1 rate.** The two NFRs have not been met simultaneously |
| FR-H2, FR-H3 | chaos runs, ablation A–C | not executed | Needs Docker |
| M6b | configuration D against Redpanda | not executed | Deferred by D-008 |

The posting path does roughly five statements per posting — idempotency claim,
posting, two entries, outbox, idempotency completion — and the zero-sum
constraint trigger aggregates per row. That is where the latency goes at rate.
It has not been optimised, because optimising against an unrepresentative
machine would be tuning to noise.

## 4. What is written but unexercised

Named plainly, because "implemented" and "exercised" are different claims.

| Component | Written and tested | Never run against the real thing |
|---|---|---|
| `internal/broker` | in-process fake, exhaustively | franz-go against Redpanda |
| `internal/ledger/consumer` | all four modes, incl. redelivery and loss | real broker, real rebalance, real offsets |
| `internal/ledger/outbox` | relay, failure mid-batch, drain on shutdown | real produce with `acks=all` |
| `internal/harness/chaos` | scheduler, validation, failure handling | real `docker kill` |
| `internal/harness/ablation` | artefact schema, fixed-parameter guard, table folding | a real run |
| `cmd/harness ablate` | preflight, orchestration, artefacts, fold (T-048/T-049) | **measured against real Redpanda v24.3.6**, 3 brokers RF=3, sweep s1788529596. Configuration D (M6b) not run |

`cmd/harness ablate` fails with a message saying exactly this rather than
producing numbers measured against something that was not the experiment.

## 5. Before the repository goes public

`make security` currently fails on two checks. Both are recorded deviations, and
both need a machine with normal network access:

1. **Compose images are tag-pinned, not digest-pinned** (D-017). Run
   `scripts/pin-digests.sh` and commit the result.
2. **`go.sum` was never generated** (D-016). The build environment could not
   reach a Go module proxy, so `go.work` supplied GitHub mirrors and the
   checksum database was disabled. Delete `go.work`, run `go mod tidy` with
   `GOSUMDB` on, and commit `go.sum`. **This is a real reduction in
   supply-chain verification until it is done.**

Then install and run the three audits the sweep currently skips: `govulncheck`,
`pip-audit`, `gitleaks`.

## 6. Design decisions a reviewer should read first

`docs/design/decisions.md`, D-001 … D-019. The four that most shape the result:

- **D-010** — two demo windows. A 30-business-day window cannot contain both an
  October holiday and a February leap day, and anchoring naively on the leap day
  puts the month boundary on a Wednesday where Q12 cannot fire. Without this,
  three quirks report as undetected for calendar reasons.
- **D-012** — both ingress paths, HTTP for Finding 1 and the topic for Finding 2,
  behind one posting service.
- **D-015** — the LLD's schema was executed against PostgreSQL 16 *before* the
  design was approved. It caught a mistyped `BIGSERIAL` and a balance query that
  returned no row at all for an account with no checkpoint.
- **D-019** — the approved LLD pinned pgx v5.10.0, which requires Go 1.25 while
  the project floor is Go 1.23. A defect in an approved document, found only by
  building against it.

## 7. Defects found by building, not by reading

Recorded because the count is the point: a design that had only been reviewed
would have shipped every one of these.

| Where | Defect | Consequence had it shipped |
|---|---|---|
| LLD schema | `BIGGSERIAL` typo | Migrations fail entirely |
| LLD schema | balance query missing `GROUP BY`, and returning no row for an account with no checkpoint | A new account reads as "balance unknown" |
| Execution plan | five tasks scheduled in the same wave as a dependency | Work blocked on arrival |
| `accrual` | interest posted only when EOD ran ON the calendar first; 2028-04-01 is a Saturday | No April interest at all, on either side. Q12 undetectable |
| `accrual` | `isMonthEnd` compared two expressions that normalise to the same date | Month-end fees never assessed |
| `legacy-sim` | transactions the cut-off pushed past midnight were dropped, not carried | Q2 deleted data instead of delaying it; 361 timing differences read as missing records |
| `legacy-sim` | ran the documented calendar, not its own | Never worked on Columbus Day — the one day Q5 exists |
| `legacy-sim` | one sequential RNG for all transaction values | Enabling Q5 shifted every later draw; the per-quirk runs were not controlled experiments |
| `legacy-sim` | holds snapshotted at 23:59 | Both expiry rules agree by then; Q8 invisible at every grain |
| `reconcile` | transaction grain filtered per-day on both sides | A moved posting could never be matched to its counterpart |
| `reconcile` | basis rules demanded exact cross-multiplication | Never matched once, because both sides had already rounded |
| `httpapi` | every malformed request mapped to 500 | Clients told to retry requests that can never succeed |
| `harness/load` | non-atomic counter in a Targeter vegeta calls from many goroutines | Same idempotency key with a different body; the ledger's 409 looked like a ledger fault |
| test fixtures | every integration package did `DROP SCHEMA` on one shared database | Packages tore each other down in parallel; failures read as migration bugs |

## 8. Known limits kept as results

Two calibration limits were found and deliberately not engineered away, because
they are true and useful:

- **At small balances a day-count basis difference is arithmetically
  indistinguishable from a rounding difference.** The interest is too small for
  the ratio to survive rounding. The simulator now uses realistic opening
  balances so Q3 and Q6 are separable — but the limit is real, and a migration
  reconciling small accounts will hit it.
- **Quirks compound.** With all twelve enabled, Q2, Q9 and Q11 change which
  transactions land on which day, so the daily balances Q3 and Q6 accrue on are
  no longer the shadow's and their exact ratio is destroyed. Eleven of twelve are
  detected in the combined run, twelve in isolation. `FINDINGS.md` reports both.

## 9. Rollback

There is nothing to roll back: no deployment, no persistent state, no consumers.
The databases are recreated per run by design (D-014). Reverting is
`git checkout` of an earlier commit; the design history is linear and each
milestone is a commit whose message names its task IDs.

## 10. Go/no-go

Ready to ship as portfolio work, with the README, `FINDINGS.md` and this report
all stating plainly that Finding 2's numbers were not measured and NFR-1 was not
verified. Not ready to be described as a completed harness, and nothing here
should ever be described as a production system.
