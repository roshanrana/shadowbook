# 01 — Requirements

**Status:** DRAFT — awaiting owner confirmation at the Phase 0 gate.
**Source:** `docs/shadowbook-project-brief.md`. Where this document and the brief disagree, the brief wins until the owner says otherwise.

## 1. Purpose

Build a demonstrable digital-twin harness for core-ledger migration that produces two reproducible, quantitative findings: (1) time-to-discovery of undocumented legacy behaviours via continuous reconciliation, and (2) the loss/duplication/latency profile of four message-consumer delivery configurations under broker failure.

## 2. Users and actors

| Actor | Role |
|---|---|
| Owner (developer) | Builds, runs, extends; uses findings in interviews and as portfolio evidence |
| Reviewer (senior engineer at a core-banking company) | Clones, runs `make demo`, reads `FINDINGS.md`, inspects code |
| legacy-sim | Simulated incumbent core; produces transaction stream and EOD extracts with seeded quirks |
| ledger | Shadow thin ledger; consumes postings, maintains balances, publishes events |
| reconcile | Compares legacy extracts to ledger state; classifies and reports breaks |
| harness | Drives load, chaos and ablation; renders reports |

## 3. Functional requirements

### 3.1 ledger (Go)

- FR-L1 Accept postings via an HTTP (or gRPC — HLD decides) API with a mandatory client-supplied idempotency key scoped per principal. Same key + same body → identical stored response; same key + different body → rejected with a distinct error.
- FR-L2 Every posting produces ≥ 2 entries whose amounts sum to zero, written in one database transaction with the idempotency record.
- FR-L3 Entries are immutable and append-only. Reversals post contra entries and reference the original.
- FR-L4 Balances are derived from entries with periodic checkpoints; the API exposes ledger balance, available balance, and pending (holds).
- FR-L5 Holds reduce available balance, not ledger balance, and expire per a documented rule (72 h after placement).
- FR-L6 End-of-day interest accrual per product with a documented basis (ACT/365, ACT/ACT in leap years) and rounding (half-even), posted on the calendar first of the month.
- FR-L7 A transactional outbox publishes a posting event per committed posting to a Kafka-compatible topic keyed by account ID.
- FR-L8 An inbound movement consumer applies postings from a topic under a configurable delivery mode: `at-most-once`, `at-least-once`, `at-least-once+inbox`, `transactional`.
- FR-L9 Expose Prometheus metrics: posting rate, posting latency histogram, consumer lag, outbox depth, invariant status (0/1) and last check time.
- FR-L10 Graceful shutdown: stop accepting, drain in-flight, commit offsets, close — via context cancellation.
- FR-L11 Money is represented as integer minor units with explicit currency and scale. JPY has scale 0.
- FR-L12 Business date, value date and cut-off (17:00:00.000 exclusive) are explicit in the data model and API.

### 3.2 legacy-sim (Python)

- FR-S1 Generate a deterministic customer/account/transaction stream from a seed, over N simulated business days, across ≥ 2 product codes and ≥ 2 currencies (incl. JPY).
- FR-S2 Apply the twelve quirks in brief §4, each individually switchable via `quirks.yaml`.
- FR-S3 Emit end-of-day extracts as flat files with header, detail, trailer (record count, control total) — one per business day per extract type (transactions, account balances).
- FR-S4 Emit the same transactions to the ledger's inbound topic (or via the API — HLD decides) so the shadow processes the same inputs.
- FR-S5 Include a business-day calendar with holidays, and a leap year in the default 30-day window's calendar model (the sim must be runnable across a Feb 29 for Q6).
- FR-S6 Byte-identical output for identical seed and configuration.

### 3.3 reconcile (Python)

- FR-R1 Compare legacy extracts to ledger state at three grains: transaction, account-day, book control total.
- FR-R2 Classify each break as timing, model-difference, or defect using deterministic rules; age breaks across days; track to closure.
- FR-R3 Produce the time-to-discovery report: per quirk, first business day detected, grain of first detection, number of breaks at first detection, number of breaks to isolate a single cause.
- FR-R4 Handle late, redelivered, and truncated extracts (bad trailer) without crashing or double-counting.
- FR-R5 No LLM in classification. Optional narrative drafting behind a flag, off by default, off in `make check`.

### 3.4 harness (Python and/or vegeta)

- FR-H1 Open-model load generation (vegeta, per D-005) with named profiles: steady, payday spike, month-end, hot-key (two accounts ≥ 20% of traffic).
- FR-H2 Scripted chaos: kill and restart brokers on a schedule.
- FR-H3 Ablation runner: execute configurations A–D under identical seed, profile and chaos; collect sent/applied/lost/duplicated, latency percentiles, lag, invariant status, drain time.
- FR-H4 `make report` renders `reports/FINDINGS.md` from run artefacts. No hand edits.
- FR-H5 `make demo` runs the full 30-day twin scenario and prints both findings.

## 4. Non-functional requirements

| ID | Requirement | Target | Verification |
|---|---|---|---|
| NFR-1 | Steady-state throughput (single ledger instance, Ryzen box) | ≥ 2,000 postings/s | harness steady profile |
| NFR-1a | Throughput during chaos runs (3-node Redpanda + 2 Postgres + harness) | ≥ 1,000 postings/s, identical across configs A–D | ablation runner (D-007) |
| NFR-2 | p99 posting latency at steady state | ≤ 50 ms | harness |
| NFR-3 | Invariant check lag | ≤ 1 s behind head | metric |
| NFR-4 | Demo wall time (30 business days) | ≤ 5 min | `make demo` timing |
| NFR-5 | Determinism | identical outputs for identical seed | `make check` golden test |
| NFR-6 | `make check` duration | < 3 min locally; identical in CI | CI |
| NFR-7 | Coverage | ledger ≥ 85% (posting path/invariants ≥ 95%); reconcile classification ≥ 90%; legacy-sim ≥ 85% (D-011) | `make check` |
| NFR-8 | Offline | `make check` passes with no network | CI job with network disabled |
| NFR-9 | Producer durability (fixed) | acks=all, RF=3, min.insync.replicas=2, unclean election off | compose config + test |

## 5. Constraints

- Go 1.23+ and Python 3.12+/uv only. Protobuf contracts for events. Redpanda and PostgreSQL 16 via Docker Compose. No Kubernetes, no cloud, in base scope.
- Development and demo on an AMD Ryzen mini PC (Linux, no GPU).
- Portfolio work; must never be described as production. README carries an explicit statement.
- Public repository; no credentials in the tree; `.env.example` only.

## 6. Assumptions

- Redpanda's Kafka transactions support is sufficient for configuration D (verify before Phase 2).
- franz-go is the Go Kafka client — **confirmed by owner at the Phase 0 gate** (D-006).
- Three-node Redpanda plus two Postgres plus harness fits the box at the relaxed chaos target of NFR-1a — **resolved by owner at the Phase 0 gate** (D-007).

## 7. Out of scope

Real money or data; customer master, product catalogue, channels, cards, lending; auth beyond a static principal header; Kubernetes; real multi-cloud/multi-region; LLM in any decision path; stretch goals S1/S2 before Phase 7.

## 8. Risks

| Risk | Mitigation |
|---|---|
| Box can't sustain 3-node Redpanda + load at target | Relax chaos-run target; keep steady-state target; document |
| Kafka transactions on Redpanda behave differently from Apache Kafka | Verify early (M0); if unsupported, run D against Apache Kafka in a separate compose profile or document as not-run |
| Quirks too easy (all detected day 1) | Q4/Q5/Q6/Q12 are calendar-gated by design; validate cadence in M2 |
| Time — owner has interview dates | Wave plan prioritises M0–M4 for weekend 1; M5–M7 weekend 2; stretch only after |
