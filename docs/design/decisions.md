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
