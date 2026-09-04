# SHADOWBOOK — ship report

Evidence of readiness, and an honest account of what is not ready. Produced at
the Phase 7 gate; the go/no-go is the owner's.

**Recommendation: ship as a portfolio artefact. Both findings are measured.**
Finding 1 is complete and reproducible from a seed. Finding 2 was measured on
2026-09-04 against a real three-broker Redpanda v24.3.6 cluster at replication
factor 3, under the quorum-preserving chaos schedule (sweep `s1788529596`).

Two things are still short of the mark and are stated as such: configuration D
is implemented but has never run, and NFR-1's throughput target was not met on
the machine available. Neither is hidden by a passing check.

## 1. What runs

| Capability | State | Evidence |
|---|---|---|
| `make check` | green, 30s | format, lint, `mypy --strict`, `go vet`, unit, integration, `-race`, plus the generated-code and calendar-golden gates |
| `make demo` | green, ~2s | 12 of 12 quirks detected across both windows; regenerates `reports/FINDINGS.md` |
| `make coverage` | green | ledger 85.9% (target 85%), posting path 96.3% (target 95%), reconcile 97%, legacy-sim ≥85% |
| `make perf` | green at the smoke rate | p50 2.3ms, p95 3.1ms, p99 3.7ms at 200 postings/s |
| `make report` | green | deterministic (asserted byte-for-byte); renders both findings, and refuses artefacts that are simulated, undrained, or from a different sweep |
| `go run ./cmd/ledger` | green | migrations apply at start-up, `/readyz` reports the live invariant, `/metrics` serves |
| `make ablate` | green against a real cluster | 9 runs, 36,000 movements each, all drained; four chaos events executed per run |
| `make ablate-sim` | green with no Docker | same sweep against an in-process multi-broker cluster; results labelled `simulated` and refused as Finding 2 (D-023, D-024) |
| `make security` | **fails on one check** | compose digest pins; see §5 |

## 2. Findings

**Finding 1 — complete.** All twelve seeded quirks are detected. Each is measured
in isolation against a control run with every quirk disabled, which is the only
way "business days until this surfaced" means what it says. A combined run with
all twelve enabled is reported alongside and detects **eleven** — quirks
compound, and that gap is itself a result.

**Finding 2 — measured (A, B, C).** Three brokers at RF=3, brokers killed and
restarted on schedule, 36,000 movements per run, three runs per configuration,
every run drained.

| Config | Applied | Lost | Duplicated | Invariant |
|---|---|---|---|---|
| A | 36000 [35975–36000] | **0 [0–25]** | 0 [0–8472] | held |
| B | 36000 | 0 | 4959 [0–8950] | held |
| C | 36000 | 0 | **0** | held |

**A is the row worth reading twice: it both lost and duplicated.** It is the
only configuration that lost anything — 25 movements produced, acknowledged by
the cluster, never applied — which is at-most-once doing exactly what it
promises. It also duplicated, because the promise holds only while the offset
commit survives and a coordinator failover can lose it. C duplicated nothing in
any run, suppressed by `inbox_pkey`: a database constraint, not a race that
usually goes the right way. **The zero-sum invariant held in all nine runs** —
duplication is a correctness failure the invariant cannot catch, which is
precisely why it needed measuring.

Configuration D is implemented and has never run: see §4.

## 3. Requirements not met

| ID | Target | Actual | Assessment |
|---|---|---|---|
| NFR-1 | ≥ 2,000 postings/s | ~1,584/s saturated in this environment | **Not verified.** Unmeasured on the target machine. Every request succeeded and the invariant held throughout, so this is capacity, not correctness |
| NFR-2 | p99 ≤ 50ms | 3.7ms at 200/s; 164ms at 1,000/s; 255ms at 2,000/s offered | **Met at low rate, not at the NFR-1 rate.** The two NFRs have not been met simultaneously |
| NFR-1a | ≥ 1,000 movements/s during chaos | run at 200/s | **Not met, and the reason is measured.** The consumer applies ~280 movements/s on a Docker Desktop / WSL2 host, so 1,000/s builds a backlog it cannot clear. The ablation compares configurations against each other under identical chaos, so a lower shared rate is a valid experiment — running above what the consumer sustains and reporting the backlog as loss would not be (D-030) |
| FR-H2, FR-H3 | chaos runs, ablation A–C | **executed** | 9 runs against real Redpanda; four chaos events per run, executed on schedule with no errors |
| M6b | configuration D against Redpanda | implemented, **never run** | D-032. `kfake` cannot verify it (no transactional producer ids), so only a real cluster can |

The posting path does roughly five statements per posting — idempotency claim,
posting, two entries, outbox, idempotency completion — and the zero-sum
constraint trigger aggregates per row. That is where the latency goes at rate.
It has not been optimised, because optimising against an unrepresentative
machine would be tuning to noise.

## 4. What is written but unexercised

Named plainly, because "implemented" and "exercised" are different claims.

Most of this table has now been discharged. What remains is one row, and it is
named rather than buried:

| Component | State |
|---|---|
| `broker.KafkaTransactionalConsumer` (configuration D) | **Implemented, never run.** `kfake` returns `UNKNOWN_SERVER_ERROR` for any transactional producer id — its `handleInitProducerID` carries a literal `// TODO: Transactional IDs` — so nothing local can exercise it. The test skips with that reason rather than passing against a path the broker never took (D-032) |

Everything else in the previous version of this table — franz-go against a real
broker, real rebalances, real offset commits, real `docker kill`, a real
ablation run — was exercised in the 2026-09-04 sweep.

**What that sweep cost is itself part of the evidence.** Nine defects surfaced
the first time the system met a real cluster, none of which any test could have
reached: five in a chaos profile that had never once been started (container
names the schedule could not match, advertised listeners unreachable from the
host, four inert cluster properties and one fatal one, and credentials that
broke the documented setup path), and four in the harness (fatal handling of
retriable commit errors, a drain detector that counted a working consumer's
backlog as loss, sweep-to-sweep contamination through reused topics, and a
duplication count that was identically zero for the configurations without an
inbox). Every one produced results that were internally consistent and wrong.
Three would have shipped as findings had the arithmetic not been impossible.

## 5. Before the repository goes public

1. **Compose images are tag-pinned, not digest-pinned** (D-017). Run
   `scripts/pin-digests.sh` on a machine with registry access and commit the
   result. This is the one remaining `make security` failure.
2. ~~`go.sum` was never generated~~ — **done** (D-027). `go mod tidy` ran with
   the checksum database enabled on 2026-09-04. It raised the Go floor from
   1.23 to 1.23.8, because franz-go's `kfake` and `kadm` declare it.

Then install and run the three audits the sweep still skips: `govulncheck`,
`pip-audit`, `gitleaks`.

## 6. Design decisions a reviewer should read first

`docs/design/decisions.md`, D-001 … D-033. The four that most shape the result:

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
