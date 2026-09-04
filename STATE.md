# STATE.md — SHADOWBOOK

> Living ledger for the `enterprise-dev-lifecycle` skill. Read this first every session. Update it every time a phase, task, or blocker changes.

## Phase

**Phases 0-3: COMPLETE.** Requirements, HLD, LLD and the execution plan were all
approved by the owner on 2026-09-03. Decisions D-001 … D-033 in
`docs/design/decisions.md`.

**Phases 4-7: COMPLETE.** Both findings are measured. Finding 2 ran against a
real three-broker Redpanda v24.3.6 cluster on 2026-09-04 (sweep `s1788529596`).
`docs/ship-report.md` is the go/no-go and remains the honest account: two things
are short of the mark and are named there rather than hidden — configuration D
is implemented but has never run, and NFR-1's throughput was not met on the
available hardware.

| Milestone | State |
|---|---|
| M-G guardrails | done — `make check` green in 30s, CI is a thin wrapper |
| M0 walking skeleton | done — posting -> outbox -> consumer -> reconciled account-day |
| M1 ledger | done — idempotency, holds, accrual, fees, EOD, four consumer modes, invariant metric |
| M2 legacy-sim | done — twelve quirks, deterministic extracts, two windows, reachability guard |
| M3 reconcile | done — three grains, classification, ageing, **Finding 1: 12 of 12 detected** |
| M4 demo | done — `make demo` in ~2s, README, runbook, generated `FINDINGS.md` |
| M5 harness | done — vegeta profiles, chaos scheduler, ablation artefacts, all tested |
| M6 Finding 2 | **done** — measured against real Redpanda: A lost 25 movements and duplicated up to 8,472; B duplicated up to 8,950 and lost nothing; C did neither. Invariant held in all nine runs |
| M6b configuration D | **implemented, never run** — `kfake` has no transactional producer ids, so only a real cluster can verify it (D-032) |
| M7 hardening | done — coverage gate, security sweep, perf smoke, ship report |

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

- **Now:** owner go/no-go on `docs/ship-report.md`. Both findings are measured
  and the repository is coherent; what remains is optional.
- **Next, in order of value — all optional, none blocking:**
  1. **`scripts/pin-digests.sh`** (D-017) on a machine with registry access.
     The single remaining `make security` failure, and the one thing that
     should be done before the repo is public.
  2. **Run configuration D** (M6b, D-032): add `--configs A,B,C,D` to the
     ablate command against the real cluster. It is the only unexercised code
     in the tree, and one sweep both verifies it and completes the table.
  3. **Re-run the sweep to populate latency for C** — the measurement landed
     after the 2026-09-04 sweep, so the committed table shows loss and
     duplication but no timings. Combines with (2) into one run.
  4. **Measure NFR-1 on real hardware**: `SHADOWBOOK_PERF_RATE=2000 make perf`.
     Not met in the build environment (~1,584/s saturated).
- **Before sharing it for review:** `git tag round-2`. Both findings stand on their own,
  and the ship report says exactly what is and is not measured.

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

One commit per wave, each naming its task IDs. `git log --oneline` is the record.

| Tasks | Commit | Note |
|---|---|---|
| T-001…T-003 | scaffold, `make check`, CI | check green in 13s on an empty tree |
| T-004 | compose profiles | images tag-pinned; digests need a networked machine (D-017) |
| T-005…T-007 | money, bizdate, protobuf | money 100% cover; bizdate 98%; gen/ diff gate wired |
| T-008 | migrations | six DDL invariants verified against live PostgreSQL 16 |
| T-009, T-010 | store, Python calendar mirror | golden-tested over all 1096 days of 2027-2029 |
| T-012…T-014, T-019, T-020 | HTTP API, outbox, metrics, balances, holds | |
| T-015, T-021…T-024 | consumer modes A-D, interest, fees, EOD | ablation claims demonstrated deterministically |
| T-027…T-034 | legacy-sim | Q10 and Q12 were undetectable as first written; both fixed |
| T-035…T-039 | reconcile, Finding 1 | six defects fixed, each of which hid a quirk |
| T-040…T-043 | FINDINGS renderer, `make demo`, README, runbook | |
| T-044…T-047 | load profiles, chaos, ablation artefacts | |
| T-048 | `make ablate` | **refuses without Docker; orchestration not implemented** |
| T-052…T-055 | security sweep, coverage gate, perf smoke, ship report | two bugs found by writing the tests |

Fourteen defects were found by running the system rather than reading it.
`docs/ship-report.md` §7 lists them with the consequence each would have had.

## Blockers

_(none — B-001 resolved 2026-09-03: the full `enterprise-dev-lifecycle` skill is installed at `.claude/skills/enterprise-dev-lifecycle/` — SKILL.md, all five `references/`, and `agents/openai.yaml` — verified byte-identical to `~/Downloads/enterprise-dev-lifecycle.zip`. Note: the copy synced to the Claude account ships SKILL.md only, so always take this skill from the repo, not the account.)_

## Deviations from approved design

| # | Deviation | Why | Reverting |
|---|---|---|---|
| D-016 | `go.mod` clean; a **gitignored `go.work`** supplies GitHub mirrors for vanity import paths | The implementation environment's egress allowlist reaches github.com but no Go module proxy | `scripts/dev-workspace.sh` regenerates it; delete `go.work` on a networked machine. **`go.sum` obligation DISCHARGED 2026-09-04 (D-027)** — `go mod tidy` ran with the checksum database on, which also raised the Go floor to 1.23.8 |
| D-017 | Compose images pinned by immutable tag, not digest | No container registry reachable | `scripts/pin-digests.sh`, one pass. **Required before the repo goes public** |
| D-018 | ~60-line embedded migrator instead of goose (supersedes part of D-013) | goose unresolvable in the environment; also one fewer dependency for a Postgres-only, forward-only need | Contained to `migrations/migrate.go` |
| D-019 | pgx v5.7.5, not the approved v5.10.0 | **Defect in the approved LLD**: v5.10.0 declares `go 1.25.0`, the project floor is Go 1.23+. They are incompatible | Either keep v5.7.5, or raise the Go floor to 1.25 and restore v5.10.0 |

| D-018 | ~60-line embedded migrator instead of goose | see above | contained to one file |
| — | `cmd/harness ablate` orchestration not implemented (T-048) | needs a Docker host to develop against | see `docs/ship-report.md` §4 |
| — | NFR-1 (≥2,000 postings/s) not verified | build environment saturates at ~1,584/s | `SHADOWBOOK_PERF_RATE=2000 make perf` on the Ryzen box |

None of these change a frozen §4 contract or any invariant. D-019 is the one that was a real defect in an approved document rather than an environment workaround.
