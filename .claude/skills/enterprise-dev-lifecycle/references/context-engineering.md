# Context Engineering — Building and Spending Context Efficiently

Read this once at the start of Phase 5, and re-skim if sessions start feeling
bloated. These rules exist because tokens spent re-discovering the project are
pure waste, and because agents drown in their own context: past a point, more
context makes output *worse*, not better.

## The economic model

Think of the lifecycle as: **think expensively once, execute cheaply many times.**
Phases 0–3 spend heavily on reasoning and write the results down. Phases 4–7
should spend almost nothing on reasoning about *what* to do — packs and specs
already say — and everything on doing it. Any time an implementation session finds
itself deliberating architecture, something upstream failed: fix the doc, not
just the moment.

## Read discipline

- **Fixed read order per session:** `STATE.md` → current task pack → files in the
  pack's scope. Everything else is on-demand and justified by a pointer in the
  pack.
- **Never** begin with repo-wide exploration, tree dumps, `git log` reading, or
  "let me get familiar with the codebase." Familiarity lives in `STATE.md`.
- **Follow pointers, not curiosity.** If the pack says "LLD §3.2", read that
  section — not the whole LLD. If a needed pointer is missing, that's a pack
  defect: add the pointer to the pack (one line), then continue.
- Load reference files from this skill only at the phase that names them.

## Write discipline

- **Targeted edits over rewrites.** Never regenerate a whole file to change ten
  lines; never retype an unchanged function.
- **Don't echo.** Never paste file contents back into the conversation to
  "confirm" them, and don't restate the plan before executing it. Act, then
  report the delta.
- **Handoff ≤10 lines.** Completion notes go in the task pack (details) and as
  ONE line in the `STATE.md` task log (summary). The next session reads the line;
  it opens the pack only if it needs the details.
- **Summarize decisions, not transcripts.** `decisions.md` entries are 5 lines.
  Nobody re-reads debates; they re-read conclusions.

## STATE.md format (repo root)

Keep it under ~60 lines by pruning: completed milestones compress to one line.

```markdown
# Project State
Updated: <date>   Phase: 5   Milestone: M2   Plan: docs/design/04-execution-plan.md

## Now
- In progress: T-014 (auth middleware) — wave 3
- Next up: T-015, T-016 (unblocked after T-014)

## Task log        ← newest first, ONE line per task
- T-013 done 2026-08-28 — order schema + migrations; note: renamed field per LLD §3.4
- T-012 done 2026-08-27 — order service CRUD
- M1 complete 2026-08-26 (T-005..T-011) ✓ all gates green
- M0 complete 2026-08-24 — walking skeleton deployed

## Blockers / open questions
- T-016 blocked: needs SMTP credentials from user (asked at M2 gate)

## Deviations from plan
- 2026-08-27: split T-010 into T-010a/b (pack exceeded scope) — decisions.md #7
```

## Question batching

Questions interrupt the user and stall agents — batch them at gates.

- Design phases: end with numbered questions, each carrying a recommended answer
  ("Q2: session length — recommend 24h sliding. Confirm?"). Recommendations turn
  open questions into confirmations, which are cheaper for everyone.
- Implementation: a mid-task question means marking the task `blocked` with the
  question written into the pack, then moving to the next unblocked task. Ask all
  accumulated questions at the next natural boundary (task done / wave done).
- Exception: irreversible or destructive operations (data deletion, spending
  money, production deploys) are asked about immediately, always.

## Scripts over reasoning

Anything deterministic and repeated belongs in a committed script, not in
per-session reasoning: quality gates (behind `make check`), code generation from
schemas, fixture/seed creation, environment setup (`make bootstrap`), release
steps. Scripts execute without consuming reasoning tokens, never drift between
sessions, and double as documentation. When you catch yourself doing the same
multi-step shell dance a second time, script it and reference it from the packs
that need it.

## Context-pack hygiene (for the orchestrator writing packs)

- A pack is a **curated** context: everything needed, nothing else. Writing a
  good pack costs the orchestrator minutes and saves every downstream reader
  the whole exploration cost — the highest-leverage token trade in this system.
- Paste only short, load-bearing excerpts (a signature, a schema); point to the
  rest by section.
- Include the *why* in one line when a constraint is non-obvious ("forward-only
  migrations — NFR-4 zero-downtime"), so workers don't burn tokens second-guessing.
- Refresh stale packs at milestone boundaries (see execution-planning.md) instead
  of letting workers reconcile contradictions at execution time.
