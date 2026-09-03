# Decisions log (append-only)

Mini-ADRs. Newest at the bottom. Never edit an accepted entry; supersede it with a new one.

---

## D-001 — Go for the ledger, Python for simulator/reconciler/reporting

**Status:** accepted (owner, brief §8)
**Context:** The project targets a Go-heavy platform role; the owner's analytical tooling and existing reconciliation code (LedgerLens) are Python.
**Decision:** Go on the hot path and I/O edge (`ledger`, consumer, outbox relay); Python for `legacy-sim`, `reconcile`, report rendering. Protobuf contracts generated for both.
**Consequences:** Two toolchains in `make check`. Contract drift is prevented by checked-in generated code and a CI diff check.

## D-002 — Redpanda as the Kafka-compatible broker

**Status:** accepted (owner, brief §8)
**Context:** Chaos runs need a three-node cluster on a mini PC. Apache Kafka in KRaft mode is viable but heavier; Redpanda is a single binary with the Kafka API.
**Decision:** Redpanda for all compose profiles. Configuration D (Kafka transactions) must be verified against Redpanda at M0; if it diverges, a secondary Apache Kafka compose profile is permitted for D only.
**Consequences:** Findings must state the broker and version. Any Redpanda-specific behaviour is called out in the methods section.

## D-003 — A simulated legacy core, not a real one

**Status:** accepted (owner, brief §2)
**Context:** No real core is available or appropriate. The findings depend on *knowing* the seeded quirks so that detection can be measured.
**Decision:** `legacy-sim` is a deterministic generator with switchable quirks and realistic extract formats (header/detail/trailer, control totals). It is honest about being a simulator.
**Consequences:** Finding 1 measures detection of *known* quirks — a calibration of the twin's reconciliation, not a discovery of unknown ones. `FINDINGS.md` says so in "what this does not prove."

## D-004 — No LLM in any decision path

**Status:** accepted (owner, brief §6)
**Context:** The platform being targeted says "deterministic where it matters." The owner's portfolio stance (LedgerLens, HARBORMASTER) is deterministic-first with bounded AI.
**Decision:** Posting, matching and classification are deterministic and unit-tested. LLM use is limited to optional narrative drafting in the report, flag-off by default and in `make check`.
**Consequences:** No model dependency in the test suite; no API keys required to run `make demo`.

## D-005 — vegeta as the load generator

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 1. k6 has first-class open-model executors but adds JavaScript as a third language to a repo that is otherwise Go + Python. vegeta is Go, matches the ledger toolchain, and keeps `make check` to two toolchains.
**Decision:** vegeta. Open-model arrival is native (`-rate`); the payday, month-end and hot-key profiles of FR-H1 are implemented as a custom `vegeta.Targeter` in Go, driven from the harness.
**Consequences:** Profile shaping is code we own and unit-test, not a k6 script — more work up front, but deterministic and testable inside `make check`, which a JS load script would not be. The HLD must specify the targeter interface and how a profile is seeded so runs stay reproducible. `SETUP.md` §1 still lists k6; it is superseded by this entry.

## D-006 — franz-go as the Go Kafka client

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 2. Configuration D requires Kafka transactions; franz-go supports them and is actively maintained. Sarama's transaction support is thinner, kafka-go has none, confluent-kafka-go pulls in cgo/librdkafka.
**Decision:** franz-go for the outbox relay, the producer and all four consumer delivery modes.
**Consequences:** Pure Go, no cgo, so `go test -race` and the offline `make check` (NFR-8) stay simple. Its transaction API must be verified against Redpanda at M0 per D-002. Verify current APIs via GitHits before coding — the brief may be stale.

## D-007 — Chaos runs target ≥ 1,000 postings/s; steady state keeps ≥ 2,000/s

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 3. Three Redpanda brokers, two Postgres instances and the harness on one Ryzen mini PC is unlikely to hold 2,000 postings/s while brokers are being killed and restarted. A target the box cannot hold produces ablation rows that measure the box, not the consumer design.
**Decision:** NFR-1 (≥ 2,000/s) applies to the single-broker steady-state measurement. New NFR-1a (≥ 1,000/s) applies to chaos and ablation runs. The rate must be *identical* across configurations A–D; the runner refuses to render a table from mismatched artefacts (see `chaos-ablation` skill).
**Consequences:** Finding 2 is a comparison between configurations at a fixed rate, not an absolute throughput claim. `FINDINGS.md` methods must state both rates and why they differ; "what this does not prove" already covers single-box numbers.

## D-008 — Configuration D may slip past weekend 2

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 4. Configurations A (at-most-once), B (naive at-least-once) and C (at-least-once + inbox) already demonstrate loss, duplication and the correct fix with its latency cost. D (Kafka transactions) adds the end-to-end-transactional comparison and carries the Redpanda-divergence risk of D-002.
**Decision:** M6 ships Finding 2 with A, B and C. D is the first item of weekend 3 and is not a weekend-2 exit criterion.
**Consequences:** The execution plan splits M6 into M6 (A–C, Finding 2) and M6b (D, Finding 2 extended). `make report` must render a valid table from three configurations and mark D as `not run` rather than failing. If the interview lands before M6b, `git tag round-2` captures A–C, which is a complete result on its own.

## D-009 — Public repository name `shadowbook`

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 5.
**Decision:** `github.com/roshanrana/shadowbook`. Go module path follows.
**Consequences:** Module path is fixed before any Go code exists, so no rename churn.

## D-010 — Two demo windows, anchored 2028-02-28 and 2028-10-02

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** Quirk cadences are calendar-gated. A 30-business-day window is ~6 calendar weeks and cannot contain both Columbus Day (Q5, October) and a leap day (Q6, February) — they are ~4.5 months apart. Anchoring naively on Feb 29 2028 also puts the month boundary on Wednesday 1 March 2028, where the calendar first *is* the first business day, so Q12 could not fire either. Left unfixed, Finding 1 reports false negatives that look like reconciliation failures.
**Decision:** `make demo` runs two windows. W1 `leap-and-month-end` = 2028-02-28 → 2028-04-07 (verified: exactly 30 business days; contains Feb 29; contains month ends Feb 29 and Mar 31; 1 April 2028 is a Saturday so Q12 diverges; nearest federal holiday Presidents Day 2028-02-21 falls outside). W2 `columbus` = 2028-10-02 → 2028-10-13 (9 business days — Columbus Day 2028-10-09 is excluded from its own window by the documented calendar, which is the point). Finding 1 gains a `window` column.
**Consequences:** A test asserts every quirk in `quirks.yaml` is reachable by at least one configured window before a run may render, so this defect cannot silently return. `quirks.yaml` is unchanged — the alternative, redefining Q5 to a holiday inside W1, was rejected as a quirk redefinition. HLD §5.5.

## D-011 — `legacy-sim` coverage target ≥ 85%

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** NFR-7 set coverage targets for `ledger` and `reconcile` but none for `legacy-sim`, whose FR-S6 byte-identical determinism the whole of Finding 1 rests on.
**Decision:** `legacy-sim` ≥ 85% lines, enforced in `make check`.
**Consequences:** NFR-7 amended in `01-requirements.md` at Phase 2.

## D-012 — legacy-sim reaches the ledger over both HTTP and the topic

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** FR-S4 left the ingress path open. Finding 1 wants the replay path a real migration would use and something a reviewer can reproduce with `curl`; Finding 2 is *about* the consumer, so postings must cross the broker for loss and duplication to mean anything.
**Decision:** Both. HTTP `POST /postings` with an idempotency key drives `make demo` and Finding 1; the movement topic drives `make ablate` and Finding 2. Both land in one posting service behind a single interface.
**Consequences:** A contract test asserts identical entries, balances and outbox rows for the same input through either path (HLD risk R6). Two ingress paths are a real cost; the test is the control.

## D-013 — Stack: net/http, pgx v5, goose, buf, client_golang, stdlib data handling, Jinja2

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** HLD §7, with registry versions verified 2026-09-03.
**Decision:** HTTP/JSON on `net/http` + stdlib mux (gRPC rejected: vegeta drives HTTP, so it would undo D-005); `jackc/pgx` v5.10.0 (typed `*pgconn.PgError` is what makes idempotency-by-constraint real); `pressly/goose` v3.28.0 with embedded plain SQL (invariants live in reviewable DDL); `buf` with generated code checked in (`buf breaking` guards the frozen contract, and check-in keeps NFR-8 offline); `prometheus/client_golang`; **no pandas or polars in `reconcile`** — stdlib `csv`/`dataclasses`/`decimal` with explicit sort keys, because deterministic ordering (NFR-5) beats speed we do not need; Jinja2 for report rendering.
**Consequences:** Two toolchains, few dependencies, everything mypy- and vet-checkable. Any later wish for a dataframe library in `reconcile` is a decision to re-open here, not a mid-task convenience.
