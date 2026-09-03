# STATE.md — SHADOWBOOK

> Living ledger for the `enterprise-dev-lifecycle` skill. Read this first every session. Update it every time a phase, task, or blocker changes.

## Phase

**Phase 0 — Intake & requirements: DRAFTED, AWAITING OWNER CONFIRMATION.**

`docs/design/01-requirements.md` has been pre-drafted from `docs/shadowbook-project-brief.md`. The Phase 0 gate is not yet passed. Next session must:

1. Present the requirements summary (≤10 lines) and the open questions listed below to the owner.
2. On confirmation, mark Phase 0 complete here and begin Phase 1 (HLD) — read `.claude/skills/enterprise-dev-lifecycle/references/design-templates.md` first.

## Open questions for the Phase 0 gate

1. Load generator: k6 (JS scripting, first-class open-model executors) or vegeta (Go, simpler, less scripting)? Default recommendation will be made in the HLD unless the owner has a preference.
2. Go Kafka client: franz-go (transactions supported, actively maintained) is the working assumption. Any objection?
3. Redpanda three-node compose for chaos runs is heavier than the box may like at 2,000 postings/s alongside two Postgres instances and the harness. Acceptable to relax the throughput target to ≥1,000/s during chaos runs, keeping ≥2,000/s for the steady-state measurement?
4. Is Finding 2's configuration D (Kafka transactions) required for weekend 2, or acceptable as the first item of a third weekend if time is short? A, B and C alone already make the point.
5. Public repo name: `shadowbook` confirmed?

## Now / next

- **Now:** Phase 0 gate — owner confirmation of requirements and answers to the five questions above.
- **Next:** Phase 1 HLD, including tech-stack recommendation (Go Kafka client, load generator, Protobuf tooling, Postgres driver, migration tool).

## Milestones (provisional — finalised in Phase 3)

| ID | Milestone | Target | Status |
|---|---|---|---|
| M0 | Walking skeleton: one posting → outbox → consumer → one reconciled account-day → one findings row | Weekend 1, day 1 | not started |
| M1 | Ledger complete: idempotency, holds, accrual, checkpoints, invariant metric | Weekend 1 | not started |
| M2 | legacy-sim with 12 quirks + deterministic extracts | Weekend 1 | not started |
| M3 | reconcile: multi-grain, classification, time-to-discovery report → **Finding 1** | Weekend 1 | not started |
| M4 | `make demo` end to end, README two-altitude, runbook | Weekend 1 | not started |
| M5 | harness: open-model load, hot keys, chaos schedule | Weekend 2 | not started |
| M6 | Ablation A–D → **Finding 2**, `make report` | Weekend 2 | not started |
| M7 | Hardening, security sweep, ship report, public | Weekend 2 | not started |
| S1/S2 | Stretch (choose one) | After M7 only | not started |

## Task log

_(empty — tasks are created in Phase 3)_

## Blockers

_(none)_

## Deviations from approved design

_(none — nothing approved yet)_
