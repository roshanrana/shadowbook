# SHADOWBOOK — Project Brief

**Working repo name:** `shadowbook`
**One line:** A digital-twin harness for core-ledger migration — a Go double-entry thin ledger run as a shadow of a simulated legacy core, continuously reconciled against it, with a chaos and load harness that proves the ledger's invariants hold under broker loss and payday load, and a report that measures how long the twin takes to discover the undocumented quirks buried in the legacy system.

**Audience for this document:** the AI coding agent (Claude Code) running the `enterprise-dev-lifecycle` skill, and the human owner. It is the Phase 0 input. It is deliberately opinionated about *what* and *why*, and deliberately silent about *how* — the HLD and LLD decide that, at their gates.

---

## 1. Why this exists

Core banking modernisation fails in the last 2% of reconciliation breaks, which are almost always undocumented legacy behaviour: a rounding convention, a cut-off time, a fee exception, a holiday calendar quirk that nobody wrote down. A digital twin — the new ledger run read-only in parallel with the incumbent, fed the same inputs, continuously compared — is the accepted tool for surfacing those behaviours before any account is migrated. Proving the twin is correct is a reconciliation problem.

Separately, any ledger that consumes postings from a message log inherits the delivery-semantics problem: at-least-once delivery plus a naive consumer is a duplicate-posting generator; at-most-once loses money. The correct pattern (idempotent processing via an inbox, or transactional consumption) is well known and rarely measured.

SHADOWBOOK builds both and measures both. It exists to produce two findings that are more interesting than the code:

1. **Time-to-discovery per legacy quirk.** For each of twelve seeded undocumented behaviours in the legacy simulator, which simulated business date the twin's reconciliation first surfaced it, at which comparison grain, and how many breaks it took to isolate the cause.
2. **Delivery-semantics ablation under broker loss.** For four consumer configurations, under identical load with brokers killed mid-run: postings lost, postings duplicated, p99 posting latency, and whether the ledger's global invariant (`SUM(debits) = SUM(credits)`) held.

The project is portfolio work built to production constraints. It is not, and must not be described as, a production system.

## 2. Scope

### In scope

- **`ledger`** — a thin, double-entry, append-only ledger service in Go. Postings API with a client-supplied idempotency key; every posting is at least two entries that sum to zero; balances are derived (with checkpoints), never mutated in place; the ledger/available/pending triple with holds that expire; end-of-day interest accrual; a transactional outbox publishing posting events to a Kafka-compatible topic keyed by account; consumer(s) of an inbound movement stream with configurable delivery semantics (see §5). Graceful shutdown via context cancellation.
- **`legacy-sim`** — a simulated legacy core (Python) that generates a deterministic, seedable customer and transaction stream, applies the twelve quirks in §4, and produces end-of-day extracts the way a real core does (flat files with header/trailer, record counts and control totals).
- **`reconcile`** — continuous multi-grain reconciliation (Python) between the legacy extracts and the shadow ledger: transaction grain, account-day grain, book-level control totals. Break classification (timing / model difference / defect), ageing, and a per-quirk discovery report.
- **`harness`** — load generation with an open-model generator (payday spike, month-end, two hot accounts concentrating ≥20% of traffic on one key), scripted chaos (broker kill on schedule), the four-configuration ablation runner, and report rendering to `reports/FINDINGS.md`.
- **`make demo`** — one command, Docker Compose, thirty simulated business days, both findings printed, in well under five minutes on the target machine.
- Design documentation per the lifecycle skill (requirements, HLD, LLD, execution plan, decisions log), a single `make check` gate (format, lint, type-check, unit, integration, race detector), CI running the same command, a runbook, and a two-altitude README.

### Out of scope (explicitly)

- Anything real-money, any real bank data, any real core system.
- Customer master data, product catalogue, channels, cards, lending. The ledger is *thin* by design — postings, balances, holds, accrual. This mirrors the stance that customer and product tooling sit outside the core.
- Authentication and authorisation beyond a static tenant/principal header.
- Kubernetes. Docker Compose is the deployment target for the demo. A Helm chart is a stretch, not a requirement.
- Real multi-cloud or multi-region deployment. See §7 for the simulated stretch.
- LLM involvement in any posting, matching, or classification decision. See §6.

## 3. Non-functional targets (demo scale)

These are targets for the demo, not production claims. They exist so the harness has something to measure against.

| Target | Value | Why |
|---|---|---|
| Sustained posting throughput on the Ryzen box | ≥ 2,000 postings/s single ledger instance | Enough to make hot-key contention and consumer lag visible |
| p99 posting latency at sustained load (no chaos) | ≤ 50 ms | A number to compare the four configurations against |
| Invariant check cadence | Continuous, ≤ 1 s behind head | The demo must show the invariant *during* chaos, not after |
| Demo wall time | ≤ 5 min for 30 business days | If it takes longer nobody watches it |
| Determinism | Same seed → byte-identical legacy extracts and identical findings tables | Findings must be reproducible by a reviewer |
| `make check` | < 3 min locally; identical in CI | Lifecycle rule 4 |

## 4. The twelve seeded quirks (legacy-sim)

Each quirk is a behaviour the legacy simulator applies silently. The shadow ledger implements the "documented" behaviour. The reconciler must surface each as a break, and the report records the business day and grain of first detection. Quirks are configurable by seed and individually switchable so the harness can measure them one at a time and all together.

| # | Quirk | Legacy behaviour | Shadow (documented) behaviour | Expected first-detection grain |
|---|---|---|---|---|
| Q1 | Interest rounding | Round half-up to cents | Round half-even | Account-day (interest accrual line) |
| Q2 | Cut-off time | Transactions at 16:59:59.999 belong to the *next* business day | Cut-off is 17:00:00.000 exclusive | Transaction (value-date mismatch) |
| Q3 | Accrual basis | ACT/360 on one product code | ACT/365 on all products | Account-day, one product only |
| Q4 | Grandfathered fee waiver | Monthly fee waived for accounts opened before 2019-01-01 | Fee applies to all | Account-day, month-end only |
| Q5 | Holiday calendar | Columbus Day is a business day | Columbus Day is a holiday | Transaction, October only |
| Q6 | Leap-day accrual | Feb 29 accrues a 366th day at ACT/365 (i.e., ACT/365 not ACT/ACT) | ACT/ACT in leap years | Account-day, leap year only |
| Q7 | Fee basis | Minimum-balance fee assessed on *ledger* balance | Assessed on *available* balance | Account-day, accounts with holds |
| Q8 | Hold expiry | Holds expire at midnight on day N+3 | Holds expire 72 hours after placement | Account-day (available balance) |
| Q9 | Reversal semantics | A reversal *deletes* the original entry | A reversal posts a contra entry | Transaction count mismatch; control totals agree |
| Q10 | Currency minor units | JPY amounts stored with two decimals and truncated | JPY has zero minor units | Transaction, JPY only |
| Q11 | Duplicate suppression window | Same amount + same counterparty within 60 s is silently dropped as a duplicate | No suppression; duplicates are the client's responsibility | Transaction (missing in legacy) |
| Q12 | Interest posting day | Interest posted on the first *business* day of the month | Posted on the calendar first | Account-day, month boundary |

The twelve are chosen so that they surface at *different* grains and *different* cadences (daily, month-end, October, leap year), which is what makes time-to-discovery a meaningful measurement rather than a flat "day 1." Q9 is designed to be invisible at control-total grain and visible only at transaction grain — that is the point.

The machine-readable seed is `legacy-sim/quirks.yaml`.

## 5. The ablation matrix (harness)

Same load profile, same seed, same chaos schedule (kill one of three brokers at t+60 s and t+150 s; restart each after 30 s). Four consumer configurations:

| Config | Offset commit | Dedupe | Expected outcome |
|---|---|---|---|
| A. at-most-once | Before processing | None | Loss under broker kill; no duplicates |
| B. naive at-least-once | After processing | None | Duplicates under broker kill; no loss |
| C. at-least-once + inbox | After processing, same DB transaction as the effect | Inbox table with unique constraint on message ID | No loss, no duplicates; measurable latency cost |
| D. transactional | Kafka transactions, `read_committed` | Producer idempotence + consumer-offset commit in transaction | No loss, no duplicates within Kafka; document exactly where the guarantee ends at the DB boundary |

Reported per configuration: postings sent, postings applied, lost, duplicated, p50/p95/p99 latency, consumer lag peak, invariant held (yes/no, and if no, at what time and by how much), time to drain after recovery.

Producer durability is fixed across configurations: `acks=all`, replication factor 3, `min.insync.replicas=2`, unclean leader election disabled. That is the money-safe configuration and it is not the variable under test.

## 6. Determinism and AI boundary

Every decision that changes a balance or classifies a break is deterministic and unit-testable. There is no LLM anywhere in the posting path, the matching path, or the classification path.

An LLM *may* be used, behind a flag that is off by default and off in `make check`, for exactly one thing: drafting a human-readable narrative for a break cluster in the findings report ("these 312 account-day breaks on product code SAV-01 are consistent with a 360-day accrual basis"). Its output is labelled as a suggestion, is never used to set the classification, and is never required for any test to pass. This is the LedgerLens stance and it is not up for revision in the HLD.

## 7. Stretch goals (choose at most one, after `make demo` works)

**S1 — Two-region ownership.** Two ledger instances; each account homed to one region; cross-region transfers as sagas with reversal entries; `tc netem` (or Toxiproxy) injecting 80 ms RTT and then a full partition. Report RPO on re-homing and saga completion time. This is the cheap, honest version of "active-active across providers" and lets the owner speak to that architecture having run it.

**S2 — Natural-language product configuration, compiled deterministically.** An LLM compiles a product description ("2.1% APY on balances over $5,000, ACT/360, fee waived for accounts opened before 2019-01-01") into a typed rule AST. The AST is schema-validated, golden-tested, hashed and signed (PROVENANCE-style attestation), and only the AST is executed by the ledger. The LLM never touches a balance. Fun, on-brand for "configuration in natural language, deterministic where it matters," and it makes the quirks in §4 expressible as product rules.

Neither stretch may be started before Phase 7 of the base scope is complete.

## 8. Environment constraints

- **Development machine:** AMD Ryzen mini PC, Linux, no GPU. Everything in the base scope runs locally in Docker Compose. Throughput targets in §3 assume this box.
- **Languages:** Go (1.23+) for the ledger and anything on the hot path or at the I/O edge. Python (3.12+, managed with `uv`) for the legacy simulator, the reconciler, and report rendering. This mirrors the HARBORMASTER split and the owner's stated position ("Go at the I/O edges, Python where the analysis lives").
- **Infrastructure:** Redpanda as the Kafka-compatible broker (three-node for the chaos runs, single-node for `make check`). PostgreSQL 16 ×2 (ledger and reconciler state). Docker Compose. No cloud dependency in the base scope.
- **Contracts:** Protobuf for event schemas, generated for both Go and Python, checked in.
- **Load generation:** k6 or vegeta (HLD decides); must be an open-model generator to avoid coordinated omission.
- **Chaos:** `docker kill` / `docker start` on a schedule from the harness; nothing exotic.
- **No network at test time.** `make check` must pass offline. Anything that needs the internet is a build step, not a test.
- **Observability:** Prometheus metrics from the ledger (posting rate, latency histogram, consumer lag, invariant status), scraped during runs; Grafana is optional for the demo but the metrics are not.

## 9. Gates and quality bar

- `make check` = format + lint + type-check (mypy strict; `go vet`) + unit tests + integration tests against a single-node compose + `go test -race` on the ledger. Green before any task is marked done. CI runs the identical command.
- Coverage targets: ledger ≥ 85% (the posting path and invariant code ≥ 95%); reconciler classification ≥ 90%.
- Adversarial scenarios, named, in the README: at minimum the twelve quirks, the four ablation configurations, a hot-key scenario, a late extract, a redelivered extract, a truncated extract with a bad trailer, and an out-of-order event.
- Every findings table in `reports/FINDINGS.md` is regenerated by `make report` from run artefacts, never hand-edited.
- Security hygiene before the repo goes public: `govulncheck`, `pip-audit`, a secrets scan, no credentials in compose files (use `.env.example`).

## 10. Deliverables

1. Working `make demo` producing both findings tables from a fixed seed.
2. `reports/FINDINGS.md` with the time-to-discovery table, the ablation table, a short methods section, and a "what this does not prove" section.
3. Design documents per the lifecycle skill under `docs/design/`.
4. `README.md` in two altitudes: one paragraph for a chief architect, one for an engineer; the named adversarial scenarios; a 90-second demo script.
5. `docs/runbook.md`: how to run, how to change the seed, how to add a quirk, how to add an ablation configuration, how to interpret each column.
6. A public GitHub repository under `github.com/roshanrana`, consistent in shape with the existing portfolio (design docs, ADRs, one `make check` gate, runbooks).

## 11. Success criterion

A senior engineer at a core-banking company clones the repo, runs `make demo`, and within five minutes sees two tables they want to argue about. The argument is the point.

## 12. Instructions to the agent

- You are running the `enterprise-dev-lifecycle` skill. This document is Phase 0 input; `docs/design/01-requirements.md` has been pre-drafted from it and awaits the owner's confirmation. Do not write application code before the Phase 3 execution plan is explicitly approved.
- Where this brief is silent, propose at the relevant gate; do not decide silently. Where this brief is explicit (§4 quirks, §5 matrix, §6 boundary, §8 languages), treat it as a constraint.
- Verify upstream before building: Redpanda's current Kafka transactions support and configuration flags, franz-go (or the chosen Go client) transactional API, k6 open-model executors. The ecosystem moves; the brief may be stale on specifics.
- Keep the walking skeleton (M0) genuinely thin: one posting, one outbox event, one consumer, one reconciled account-day, one row in a findings table. Everything else is a later wave.
- The owner is preparing for interviews on a fixed timeline. Weekend-1 scope (§2 `ledger`, `legacy-sim`, `reconcile`, `make demo` with Finding 1 only) is the priority; the harness and Finding 2 are weekend 2. Plan waves accordingly and say so in the execution plan.
