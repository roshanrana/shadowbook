# Decisions log (append-only)

Mini-ADRs. Newest at the bottom. Never edit an accepted entry; supersede it with a new one.

---

## D-001 — Go for the ledger, Python for simulator/reconciler/reporting

**Status:** accepted (owner, brief §8)
**Context:** The project targets a Go-heavy platform context; the owner's analytical tooling and existing reconciliation code (LedgerLens) are Python.
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
**Consequences:** The execution plan splits M6 into M6 (A–C, Finding 2) and M6b (D, Finding 2 extended). `make report` must render a valid table from three configurations and mark D as `not run` rather than failing. If the review window lands before M6b, `git tag round-2` captures A–C, which is a complete result on its own.

## D-009 — Public repository name `shadowbook`

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 5.
**Decision:** `github.com/roshanrana/shadowbook`. Go module path follows.
**Consequences:** Module path is fixed before any Go code exists, so no rename churn.
