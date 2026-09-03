# STATE.md — SHADOWBOOK

> Living ledger for the `enterprise-dev-lifecycle` skill. Read this first every session. Update it every time a phase, task, or blocker changes.

## Phase

**Phase 0 — Intake & requirements: COMPLETE. Approved by owner 2026-09-03.**

**Phase 1 — HLD: COMPLETE. Approved by owner 2026-09-03**, including all four §12 open questions and the full §7 stack table (D-010 … D-013).

**Phase 2 — LLD: DRAFTED, AWAITING OWNER APPROVAL.** `docs/design/03-lld.md`. Interfaces defined there are frozen on approval: changing one afterwards is an LLD change requiring sign-off and propagation to every affected task pack.

## Phase 0 questions — ANSWERED (owner, 2026-09-03)

| # | Question | Answer | Recorded as |
|---|---|---|---|
| 1 | Load generator: k6 or vegeta? | **vegeta** — Go, keeps the repo to two toolchains; profiles become a custom targeter we unit-test | D-005 |
| 2 | Go Kafka client: franz-go? | **Confirmed** | D-006 |
| 3 | Relax chaos-run throughput to ≥1,000/s? | **Yes** — NFR-1a added; ≥2,000/s still applies to steady state | D-007 |
| 4 | Is configuration D required for weekend 2? | **No** — A/B/C ship Finding 2 at M6; D becomes M6b, first item of weekend 3 | D-008 |
| 5 | Public repo name `shadowbook`? | **Confirmed** — `github.com/roshanrana/shadowbook` | D-009 |

## Carried into Phase 1

- **Accrual basis — RETRACTED, not a defect.** Flagged at the Phase 0 gate, then withdrawn on reading `legacy-sim/quirks.yaml` properly: every quirk carries a `documented:` field naming the shadow's behaviour, and Q3 (`documented: ACT/365 on all products`) and Q6 (`documented: ACT/ACT in leap years`) both agree with FR-L6. The bases do diverge as designed. The HLD still states the day-count and rounding rules in one place (`02-hld.md` §5.4) because they must live in one named module, but nothing in the requirements needed changing.
- **Real calendar defect found instead** — see `02-hld.md` §5.5 and Phase 1 open question 1. A single 30-business-day window cannot contain both Columbus Day (Q5, October) and a leap day (Q6, February); they are ~4.5 months apart. Without a fix both quirks report as undetected in Finding 1 for calendar reasons rather than reconciliation reasons.
- **NFR-7 coverage gap:** RESOLVED — `legacy-sim` ≥ 85% adopted at the Phase 1 gate (D-011).

## Now / next

- **Now:** Phase 0 gate — owner approval of `01-requirements.md`, after the accrual-basis defect above is resolved. Five questions are answered; nothing else blocks the gate.
- **Next:** Phase 1 HLD. Stack questions still open for the HLD: Protobuf tooling (buf vs protoc), Postgres driver (pgx vs database/sql), migration tool (goose vs golang-migrate vs sqlc-adjacent), HTTP vs gRPC for FR-L1, and whether legacy-sim feeds the ledger via the API or the topic (FR-S4). The Kafka client and load generator are already settled (D-005, D-006).

## Milestones (provisional — finalised in Phase 3)

| ID | Milestone | Target | Status |
|---|---|---|---|
| M0 | Walking skeleton: one posting → outbox → consumer → one reconciled account-day → one findings row | Weekend 1, day 1 | not started |
| M1 | Ledger complete: idempotency, holds, accrual, checkpoints, invariant metric | Weekend 1 | not started |
| M2 | legacy-sim with 12 quirks + deterministic extracts | Weekend 1 | not started |
| M3 | reconcile: multi-grain, classification, time-to-discovery report → **Finding 1** | Weekend 1 | not started |
| M4 | `make demo` end to end, README two-altitude, runbook | Weekend 1 | not started |
| M5 | harness: open-model load, hot keys, chaos schedule | Weekend 2 | not started |
| M6 | Ablation A–C → **Finding 2**, `make report` | Weekend 2 | not started |
| M6b | Configuration D (Kafka transactions) → Finding 2 extended | Weekend 3 (D-008) | not started |
| M7 | Hardening, security sweep, ship report, public | Weekend 2 | not started |
| S1/S2 | Stretch (choose one) | After M7 only | not started |

## Task log

_(empty — tasks are created in Phase 3)_

## Blockers

_(none — B-001 resolved 2026-09-03: the full `enterprise-dev-lifecycle` skill is installed at `.claude/skills/enterprise-dev-lifecycle/` — SKILL.md, all five `references/`, and `agents/openai.yaml` — verified byte-identical to `~/Downloads/enterprise-dev-lifecycle.zip`. Note: the copy synced to the Claude account ships SKILL.md only, so always take this skill from the repo, not the account.)_

## Deviations from approved design

_(none — nothing approved yet)_
