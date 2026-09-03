# STATE.md — SHADOWBOOK

> Living ledger for the `enterprise-dev-lifecycle` skill. Read this first every session. Update it every time a phase, task, or blocker changes.

## Phase

**Phase 0 — Intake & requirements: COMPLETE. Approved by owner 2026-09-03.**

**Phase 1 — HLD: COMPLETE. Approved by owner 2026-09-03**, including all four §12 open questions and the full §7 stack table (D-010 … D-013).

**Phase 2 — LLD: COMPLETE. Approved by owner 2026-09-03.** Interfaces in `03-lld.md` §4 are now **FROZEN** — changing one is a plan change requiring sign-off and propagation to every affected task pack. DDL verified against PostgreSQL 16.13 (§8); two defects fixed before approval (D-015).

**Phase 3 — Execution plan: DRAFTED, AWAITING OWNER APPROVAL.** `docs/design/04-execution-plan.md` plus task packs in `docs/tasks/`. **This is the hard gate: no application code may be written until the owner replies with the literal word "approved."** `docs/design/03-lld.md`. Interfaces defined there are frozen on approval: changing one afterwards is an LLD change requiring sign-off and propagation to every affected task pack.

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

- **Now:** Phase 3 hard gate — owner approval of `docs/design/04-execution-plan.md`. **No application code until the owner replies "approved."**
- **Next:** Phase 4 guardrails — wave 1 is T-001 alone (scaffold the LLD §1 tree), then wave 2 is T-002, T-003, T-004 in parallel. Read `.claude/skills/enterprise-dev-lifecycle/references/validation-shipping.md` (guardrails section) before starting T-002.
- **Plan shape:** 55 tasks, 10 milestones, 19 waves. Packs for T-001…T-008 are written in full; T-009…T-055 are stubs refined at each milestone boundary. `docs/design/04-execution-plan.md` §3 is the scheduling source of truth — this file mirrors only the current wave.

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

| # | Deviation | Why | Reverting |
|---|---|---|---|
| D-016 | `go.mod` clean; a **gitignored `go.work`** supplies GitHub mirrors for vanity import paths | The implementation environment's egress allowlist reaches github.com but no Go module proxy | Delete `go.work` on a networked machine. **Then run `go mod tidy` to regenerate `go.sum` with the checksum database on** — it was disabled during implementation |
| D-017 | Compose images pinned by immutable tag, not digest | No container registry reachable | `scripts/pin-digests.sh`, one pass. **Required before the repo goes public** |
| D-018 | ~60-line embedded migrator instead of goose (supersedes part of D-013) | goose unresolvable in the environment; also one fewer dependency for a Postgres-only, forward-only need | Contained to `migrations/migrate.go` |
| D-019 | pgx v5.7.5, not the approved v5.10.0 | **Defect in the approved LLD**: v5.10.0 declares `go 1.25.0`, the project floor is Go 1.23+. They are incompatible | Either keep v5.7.5, or raise the Go floor to 1.25 and restore v5.10.0 |

None of these change a frozen §4 contract or any invariant. D-019 is the one that was a real defect in an approved document rather than an environment workaround.
