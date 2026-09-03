# STATE.md — SHADOWBOOK

> Living ledger for the `enterprise-dev-lifecycle` skill. Read this first every session. Update it every time a phase, task, or blocker changes.

## Phase

**Phase 0 — Intake & requirements: QUESTIONS ANSWERED, AWAITING OWNER APPROVAL OF THE REQUIREMENTS DOCUMENT.**

`docs/design/01-requirements.md` was pre-drafted from `docs/shadowbook-project-brief.md` and has been updated with the owner's Phase 0 answers (D-005 … D-009). The gate is **not yet passed**: the owner answered the five open questions on 2026-09-03 but has not yet approved the requirements document itself.

To pass the gate, the next session must:

1. Resolve the one open requirements defect below (accrual basis).
2. Obtain the owner's explicit approval of `01-requirements.md`.
3. Mark Phase 0 complete here, then begin Phase 1 (HLD) — read `.claude/skills/enterprise-dev-lifecycle/references/design-templates.md` first. **That file does not exist yet** (see Blockers).

## Phase 0 questions — ANSWERED (owner, 2026-09-03)

| # | Question | Answer | Recorded as |
|---|---|---|---|
| 1 | Load generator: k6 or vegeta? | **vegeta** — Go, keeps the repo to two toolchains; profiles become a custom targeter we unit-test | D-005 |
| 2 | Go Kafka client: franz-go? | **Confirmed** | D-006 |
| 3 | Relax chaos-run throughput to ≥1,000/s? | **Yes** — NFR-1a added; ≥2,000/s still applies to steady state | D-007 |
| 4 | Is configuration D required for weekend 2? | **No** — A/B/C ship Finding 2 at M6; D becomes M6b, first item of weekend 3 | D-008 |
| 5 | Public repo name `shadowbook`? | **Confirmed** — `github.com/roshanrana/shadowbook` | D-009 |

## Open requirements defect (must be resolved before the gate passes)

- **Accrual basis is ambiguous.** FR-L6 gives the shadow ledger ACT/365 (ACT/ACT in leap years). Q3 seeds `accrual_basis_act360_on_product` and Q6 seeds `leap_day_accrual_act365` as *legacy* quirks. If the shadow's own basis is not named unambiguously and separately from each quirk's basis, Q3 and Q6 may not actually diverge from the shadow and would show as undetectable in Finding 1. The shadow's basis, the per-product override rule, and the rounding mode must be stated once, in one place, in the HLD.
- **Minor:** NFR-7 sets coverage targets for `ledger` and `reconcile` but none for `legacy-sim`. Propose ≥ 85% given FR-S6 (byte-identical determinism) depends on it.

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

- **B-001 — `enterprise-dev-lifecycle/references/` is missing.** `.claude/skills/enterprise-dev-lifecycle/SKILL.md` is installed (copied from the account-synced skill, which ships SKILL.md only). The five reference files are absent: `design-templates.md` (needed at Phase 1 **and** Phase 2), `execution-planning.md` (Phase 3), `context-engineering.md` and `orchestration.md` (Phase 5), `validation-shipping.md` (Phases 4, 6, 7). **Blocks Phase 1.** Fix: unzip `enterprise-dev-lifecycle.zip` into that directory, then delete its README.

## Deviations from approved design

_(none — nothing approved yet)_
