# Execution Plan — SHADOWBOOK

Status: **approved 2026-09-03**; implemented through M7 — see `../ship-report.md`
LLD: `docs/design/03-lld.md` (approved 2026-09-03, interfaces frozen) · Decisions: `docs/design/decisions.md`

> **Hard gate.** No application code is written until the owner replies "approved" to this plan (`enterprise-dev-lifecycle` rule 1).

## 1. Shape of the plan

55 tasks, 10 milestones, 19 waves. Weekend 1 is M0–M4 and ends with a demoable Finding 1; weekend 2 is M5–M7 and ends with Finding 2; M6b is weekend 3 by D-008. The owner's priority is explicit in CLAUDE.md — **optimise for a demoable slice first** — so the wave schedule front-loads the vertical slice and defers everything the demo does not need.

Two structural choices worth stating:

- **Guardrails are M-G, before M0.** Phase 4 of the lifecycle says formatter, linter, type checker, test harness, one `check` command and CI exist *before* any feature code. Four tasks, one evening.
- **After M0, every milestone is a vertical slice**, not a layer. M1 finishes the ledger, M2 the simulator, M3 the reconciler — but M0 has already crossed all three, so integration risk is retired while the codebase is small.

## 2. Milestones

| ID | Name | Demonstrates | Gate |
|---|---|---|---|
| M-G | Guardrails | `make check` exists and fails loudly; CI runs it | `make check` green on an empty tree |
| M0 | Walking skeleton | one posting → outbox → consumer → reconciled account-day → one findings row | `make check` green + e2e test passes |
| M1 | Ledger complete | idempotency, holds, accrual, fees, checkpoints, four consumer modes, invariant metric | `make check` + all named ledger scenarios |
| M2 | legacy-sim | twelve switchable quirks, deterministic extracts, both ingress paths | golden extracts byte-identical; every quirk reachable |
| M3 | reconcile → **Finding 1** | three grains, classification, ageing, time-to-discovery | Finding 1 table renders for W1 and W2 |
| M4 | `make demo` | 30+9 business days, both windows, under five minutes | `make demo` green from clean checkout |
| M5 | harness | open-model load, four profiles, hot keys, chaos schedule | load holds NFR-1; chaos schedule executes |
| M6 | Ablation A–C → **Finding 2** | lost/duplicated/latency under identical chaos | `make ablate` + `make report` deterministic |
| M6b | Configuration D | Kafka transactions vs Redpanda (D-008, weekend 3) | D row renders or is marked `not run` |
| M7 | Hardening & ship | security sweep, coverage, perf smoke, ship report | `docs/ship-report.md` + owner go/no-go |

## 3. Task table

Scheduling source of truth. `STATE.md` mirrors only the current wave.

| ID | Title | M | Wave | Depends on | Size | Status |
|---|---|---|---|---|---|---|
| T-001 | Scaffold repository layout per LLD §1 | M-G | 1 | — | M | done |
| T-002 | Toolchain config and the single `make check` | M-G | 2 | T-001 | M | done |
| T-003 | CI workflow as a thin wrapper around `make check` | M-G | 2 | T-001 | S | done |
| T-004 | Compose profiles, pinned images, Prometheus scrape | M-G | 2 | T-001 | M | done |
| T-005 | `internal/money` — Amount, currency registry, scale | M0 | 3 | T-002 | S | done |
| T-006 | `internal/bizdate` — calendar, cut-off, day count, rounding | M0 | 3 | T-002 | M | done |
| T-007 | Protobuf contracts, buf config, generated code checked in | M0 | 3 | T-002 | M | done |
| T-008 | Ledger migrations 0001–0006 with DDL invariants | M0 | 3 | T-002 | M | done |
| T-009 | `internal/ledger/store` — pgx pool, goose embed, queries | M0 | 4 | T-005, T-008 | M | done |
| T-010 | `legacy_sim.calendar` mirror + golden test vs bizdate | M0 | 4 | T-006 | S | done |
| T-011 | Posting service: idempotency by constraint, zero-sum | M0 | 5 | T-009 | L | done |
| T-012 | HTTP API handlers and error taxonomy | M0 | 6 | T-011 | M | done |
| T-013 | Outbox relay to Redpanda | M0 | 5 | T-009, T-007 | M | done |
| T-014 | Metrics and the global-invariant checker | M0 | 5 | T-009 | M | done |
| T-015 | Consumer, mode C only (inbox dedup) | M0 | 6 | T-013 | M | done |
| T-016 | legacy-sim minimal: one account, one day, one TXN extract | M0 | 7 | T-010, T-012 | M | done |
| T-017 | reconcile minimal: ingest, account-day grain, one findings row | M0 | 8 | T-016 | M | done |
| T-018 | M0 end-to-end walking-skeleton test | M0 | 9 | T-014, T-015, T-017 | M | done |
| T-019 | Derived balances and checkpoints | M1 | 10 | T-018 | M | done |
| T-020 | Holds: place, release, 72-hour expiry | M1 | 11 | T-019 | M | done |
| T-021 | Interest accrual: ACT/365, ACT/ACT, half-even | M1 | 11 | T-019, T-006 | M | done |
| T-022 | Fees: monthly fee and minimum-balance fee on available | M1 | 12 | T-020 | M | done |
| T-023 | EOD orchestration, ordering, replay idempotence | M1 | 13 | T-021, T-022 | M | done |
| T-024 | Consumer modes A, B and D | M1 | 10 | T-015 | L | done |
| T-025 | Graceful shutdown and cancellation audit | M1 | 11 | T-024 | S | done |
| T-026 | Named ledger scenarios: idempotency race, out-of-order | M1 | 14 | T-023 | M | done |
| T-027 | legacy-sim generator: accounts, products, currencies | M2 | 10 | T-016 | M | done |
| T-028 | Quirks Q1–Q4 | M2 | 11 | T-027 | M | done |
| T-029 | Quirks Q5–Q8 | M2 | 11 | T-027 | M | done |
| T-030 | Quirks Q9–Q12 | M2 | 11 | T-027 | M | done |
| T-031 | Extract writer: TXN and BAL, trailer control totals | M2 | 11 | T-027 | M | done |
| T-032 | Dual ingress (HTTP + topic) and the equivalence contract test | M2 | 12 | T-031, T-013 | M | done |
| T-033 | Determinism golden test over W1 and W2 extracts | M2 | 12 | T-028, T-029, T-030, T-031 | S | done |
| T-034 | Window config and the quirk-reachability guard | M2 | 13 | T-033 | S | done |
| T-035 | reconcile ingest: trailer, late, redelivered, truncated | M3 | 12 | T-017 | M | done |
| T-036 | Three grain comparators | M3 | 13 | T-035 | M | done |
| T-037 | Classification rules and the model-rule table | M3 | 14 | T-036 | L | done |
| T-038 | Break ageing, closure and history | M3 | 14 | T-036 | M | done |
| T-039 | Quirk attribution and time-to-discovery metrics | M3 | 15 | T-037, T-038, T-034 | L | done |
| T-040 | Finding 1 rendering into the FINDINGS template | M3 | 16 | T-039 | M | done |
| T-041 | `make demo`: both windows, end to end, under five minutes | M4 | 17 | T-040, T-023 | M | done |
| T-042 | README two-altitude refresh and scenario→test map | M4 | 18 | T-041 | S | done |
| T-043 | Runbook | M4 | 18 | T-041 | S | done |
| T-044 | vegeta pacers and targeters for four profiles | M5 | 10 | T-012 | M | done |
| T-045 | Chaos scheduler: scripted broker kill and restart | M5 | 10 | T-004 | M | done |
| T-046 | Ablation runner and run artefacts | M5 | 11 | T-044, T-045, T-024 | L | done |
| T-047 | Loss and duplication measurement, drain logic | M6 | 14 | T-046 | M | done |
| T-048 | `make ablate` for A–C, three runs each | M6 | 15 | T-047 | M | done |
| T-049 | Finding 2 rendering with the fixed-parameter guard | M6 | 16 | T-048 | M | done |
| T-050 | `make report` determinism test | M6 | 17 | T-049, T-040 | S | done |
| T-051 | Configuration D against Redpanda, with go/no-go | M6b | 18 | T-049 | L | implemented, unverified (D-032) — A/B/C measured against real Redpanda |
| T-052 | Security sweep: govulncheck, pip-audit, secrets scan | M7 | 18 | T-050 | M | done |
| T-053 | Coverage enforcement to NFR-7 targets | M7 | 18 | T-050 | M | done |
| T-054 | Performance smoke against NFR-1 and NFR-2 | M7 | 18 | T-050 | M | done |
| T-055 | Ship report and public-repo readiness | M7 | 19 | T-052, T-053, T-054 | M | done |

## 4. Dependency notes

Only the non-obvious edges.

- **T-011 → T-009**: the posting service depends on the store exposing `pgx` errors untranslated, so it can branch on SQLSTATE `23505` and the constraint name. If the store wraps errors into its own taxonomy, idempotency-by-constraint breaks.
- **T-016 → T-012**: the minimal simulator posts over HTTP, so it needs the API before it can exist at all. This is deliberate — it forces the M0 slice to cross a real network boundary.
- **T-024 → T-015**: modes A, B and D are variations on the consumer loop written in T-015. Writing C first is intentional: it is the only mode that is *correct*, so the others are expressed as documented weakenings of it rather than four parallel implementations.
- **T-032 → T-013**: the topic ingress needs the outbox relay's producer configuration (NFR-9 durability settings) to already exist so both producers are configured identically.
- **T-039 → T-034**: time-to-discovery is meaningless unless every quirk is reachable by a configured window; the reachability guard must exist first or Finding 1 silently under-reports (D-010).
- **T-044 → T-012**: pacers and targeters drive the HTTP API, so the route shapes must be frozen first. They are, at LLD §3.4.
- **T-051 → T-049**: configuration D must slot into a rendering path that already handles a missing row, so `make report` must be able to mark D `not run` before D exists.

## 5. Wave schedule

A wave is a set of tasks with satisfied dependencies and disjoint file scopes, runnable in parallel.

```
Weekend 1 — evening (guardrails)
  Wave 1  T-001
  Wave 2  T-002  T-003  T-004
Weekend 1 — day 1 (M0 walking skeleton)
  Wave 3  T-005  T-006  T-007  T-008
  Wave 4  T-009  T-010
  Wave 5  T-011  T-013  T-014
  Wave 6  T-012  T-015
  Wave 7  T-016
  Wave 8  T-017
  Wave 9  T-018   <- M0 GATE
Weekend 1 — day 2 (M1 ledger, M2 simulator, M5 harness in parallel)
  Wave 10 T-019  T-024  T-027  T-044  T-045
  Wave 11 T-020  T-021  T-025  T-028  T-029  T-030  T-031  T-046
Weekend 1 — day 3 (M3 reconcile)
  Wave 12 T-022  T-032  T-033  T-035
  Wave 13 T-023  T-034  T-036
  Wave 14 T-026  T-037  T-038  T-047
  Wave 15 T-039  T-048
Weekend 2 (M4 demo, M6 Finding 2)
  Wave 16 T-040  T-049
  Wave 17 T-041  T-050
Weekend 3 (M6b config D, M7 ship)
  Wave 18 T-042  T-043  T-051  T-052  T-053  T-054   <- M4 GATE, Finding 1 demoable
  Wave 19 T-055   <- SHIP GATE
```

Wave 11 is the widest at 8 tasks (T-020, T-021, T-025, T-028, T-029, T-030, T-031, T-046). Their file scopes are disjoint by construction — different packages, no shared file.

## 6. Gate map

| Level | When | What runs |
|---|---|---|
| Per task | End of every task | `make check` — format, lint, mypy strict, go vet, unit, integration, `-race` |
| Per milestone | M0, M1, M2, M3, M4, M6 | `make check` + that milestone's named scenario tests + its golden files |
| Finding gates | M3, M6 | Finding renders deterministically twice from the same seed |
| Pre-ship | M7 | `govulncheck`, `pip-audit`, secrets scan, coverage to NFR-7, perf smoke to NFR-1/NFR-2 |

## 7. Risks carried into implementation

| # | Risk | Where it bites | Carried mitigation |
|---|---|---|---|
| R1 | Calendar windows make Q5, Q6 or Q12 unreachable, so Finding 1 shows false negatives | T-034, T-039 | Reachability guard is a task, and T-039 depends on it |
| R2 | Redpanda transaction semantics diverge, breaking configuration D | T-051 | Off the critical path by D-008; `make report` renders D as `not run` |
| R3 | Box cannot sustain three brokers plus two Postgres plus load | T-046, T-048 | NFR-1a relaxed rate (D-007); reduce account count before rate, since rate equality is what the finding needs |

## 8. Plan consistency — checked, not assumed

The task table, the §5 wave schedule and all 55 packs are generated and validated against each other by script, not by eye. The checks that run:

| Check | Result |
|---|---|
| Every task has a pack; every pack has a task | pass (55 / 55) |
| Every dependency names a real task | pass |
| Every task's wave is strictly greater than all of its dependencies' | pass |
| No dependency cycles | pass |
| §5 schedule agrees with the table for all 55 tasks | pass |
| Every pack's `Milestone:` / `Wave:` header agrees with the table | pass |
| No M1, M2 or M5 task begins at or before the M0 gate | pass |

Waves are computed as `max(milestone floor, 1 + latest dependency wave)`, not assigned by hand. The first hand-written schedule contained five ordering defects — T-020 scheduled alongside T-019 which it depends on, and the same for T-026/T-023, T-034/T-033, and T-042 and T-043 against T-041 — plus two places where §5 and the table disagreed. A dependency-only recomputation then over-corrected, pulling M5 harness work in front of the M0 gate, which is why the milestone floor is part of the formula. Re-run the checker after any edit to this table.

## 9. Task packs

Packs for waves 1–3 (T-001 … T-008) are written in full in `docs/tasks/`. Later packs are stubs carrying goal, scope and dependencies, and are refined at each milestone boundary — details learned in M0 and M1 routinely rewrite them, and paying for that detail now is waste.
