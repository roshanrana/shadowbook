# SHADOWBOOK — instructions for Claude Code

You are working in `shadowbook`, a digital-twin harness for core-ledger migration. Read this file, then `STATE.md`, then stop reading and start working. Do not crawl the repo.

## Process

This project runs under the `enterprise-dev-lifecycle` skill (`.claude/skills/enterprise-dev-lifecycle/`). Its rules are binding here:

1. **No application code before the Phase 3 execution plan is explicitly approved** by the owner with the literal word "approved."
2. **All state lives in files** — `STATE.md`, `docs/design/*`, `docs/tasks/*`. Assume the chat is gone next session.
3. **`make check` is the only definition of done.** It must be green before a task is marked complete. CI runs the same command.
4. **Stay in scope.** A task touches only the files in its pack. If that is impossible, stop, amend the plan, record the deviation in `STATE.md`.
5. **Two strikes.** A gate that fails twice stops the task; write findings into the task pack, mark it `blocked`, move on.

The project brief is `docs/shadowbook-project-brief.md`. It is Phase 0 input. Where it is explicit, it is a constraint; where it is silent, propose at the relevant gate.

## Non-negotiables specific to this project

- **Double-entry, append-only, derived balances.** Every posting is ≥ 2 entries summing to zero. Nothing updates or deletes an entry. Reversals are contra entries. Balances are computed from entries (with checkpoints), never stored as the source of truth.
- **The global invariant is always checkable:** `SUM(all entries) = 0`, and per account `balance = SUM(entries) since checkpoint + checkpoint`. The ledger exposes this as a metric and a test asserts it after every scenario.
- **Idempotency is a database constraint, not application logic.** Idempotency keys and inbox message IDs live in tables with unique constraints, written in the same transaction as their effect.
- **No LLM in any posting, matching, or classification path.** The only permitted LLM use is narrative drafting in the report, behind a flag that is off by default and off in `make check`. See brief §6.
- **Determinism.** Same seed → identical legacy extracts and identical findings tables. Any non-determinism in the simulator or reconciler is a bug.
- **Money is never a float.** Integer minor units with an explicit currency and scale, or `NUMERIC` with explicit precision. Rounding rules live in one place and are named.
- **Business date ≠ wall-clock date.** Value date, posting date and cut-off are explicit everywhere.
- **Findings are generated, never hand-edited.** `reports/FINDINGS.md` is produced by `make report` from run artefacts.

## Languages and layout

- **Go 1.23+** for `ledger` and anything on the hot path or at the I/O edge. Table-driven tests; `go test -race` in `make check`; `errgroup` for fan-out; `ctx.Done()` in every blocking select; no goroutine without a cancellation path.
- **Python 3.12+ / uv** for `legacy-sim`, `reconcile`, and report rendering. `ruff` + `mypy --strict`. `pytest`.
- **Protobuf** for event contracts, generated for both languages, checked in.
- **Redpanda** (Kafka API) via Docker Compose; **PostgreSQL 16**.
- Repository layout is decided in `docs/design/03-lld.md` (Phase 2). Do not create service directories before then.

## Conventions carried over from the owner's other repos

- Design docs and ADRs are first-class. `docs/design/decisions.md` is append-only.
- Named adversarial scenarios in the README; each maps to at least one test.
- One `make check`; CI is a thin wrapper around it.
- Runbook before ship.
- Commit messages carry the task ID (`T-012: …`).

## Project-local skills

- `.claude/skills/ledger-invariants/` — how to write and test posting-path code so the invariants above cannot be violated. Read when a task touches the ledger.
- `.claude/skills/chaos-ablation/` — how to run the four-configuration ablation reproducibly and what to record. Read when a task touches the harness.
- `.claude/skills/findings-report/` — how `reports/FINDINGS.md` is structured and regenerated. Read when a task touches reporting.

## Tooling available

- Engineering plugin: `architecture` (ADRs), `testing-strategy`, `system-design`, `code-review`. Use `architecture` when adding to `decisions.md`.
- CockroachDB plugin: relevant only for stretch S1 (multi-region patterns). Do not introduce CockroachDB into the base scope.
- GitHits MCP (if connected): use it to verify current franz-go, Redpanda, k6 APIs before coding against them. The brief may be stale on specifics.

## Owner context

The owner is a senior fintech implementation engineer with deep reconciliation and collateral experience, preparing for a Forward Deployed Engineer interview at a core-banking platform company. Weekend-1 scope (ledger + legacy-sim + reconcile + Finding 1) is the priority. Optimise the plan for a demoable slice first; the harness and Finding 2 follow. Say this explicitly in the execution plan's wave schedule.
