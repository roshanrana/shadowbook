# Execution Planning — Decomposition, Task Packs, Waves

Read this when entering Phase 3. Output: `docs/design/04-execution-plan.md` plus
one task pack per task under `docs/tasks/`. This phase converts the approved
designs into units of work small enough that a single agent session can complete
one reliably, cheaply, and verifiably.

## Why chunking this way matters

Agent reliability falls off a cliff as task size grows: long tasks accumulate
context, drift from specs, and produce failures that are expensive to diagnose.
Small tasks with pre-built context also cost far fewer tokens — the agent reads a
150-line pack instead of exploring a repo. Decomposition is where both quality
and token efficiency are actually won.

## Decomposition rules

A valid task:

1. **Is atomic** — completable in one focused agent session. Heuristics: ≤ ~400
   changed lines, ≤ ~8 files in scope, one concern. If a task description needs
   the word "and" between two deliverables, split it.
2. **Is independently verifiable** — has acceptance criteria checkable by command
   or direct inspection, without finishing other tasks first (its dependencies
   excepted).
3. **Has an exhaustive file scope** — every file it may create or modify, listed.
   This enables parallelism (disjoint scopes never conflict) and prevents drift.
4. **States its contracts** — the exact interfaces (from LLD §4 or the relevant
   component section) it must implement or consume, pasted into the pack if short,
   referenced by section if long.
5. **Ends in a gate** — names the exact validation commands that prove it done.

Ordering rules:

- **M0 is the walking skeleton**: the thinnest end-to-end slice through the real
  architecture — one trivial feature crossing every layer (UI/API → logic → store
  → back), deployed through the real pipeline. It flushes out integration and
  tooling risk while the codebase is tiny and cheap to change.
- After M0, prefer **vertical slices** (a full feature through all layers) over
  horizontal layers ("build all models, then all services…"). Vertical slices are
  demoable, testable, and keep integration risk continuously retired.
- Spike tasks (time-boxed investigations) are allowed for genuinely unknown
  territory; their deliverable is a decision written to `decisions.md`, not code.

## Execution plan document template

```markdown
# Execution Plan — <project>
Status: draft | approved <date>

## Milestones
| ID | Name | Demonstrates | Gate |
| M0 | Walking skeleton | e2e slice through real pipeline | check green + deployed |
| M1 | ... | | |

## Task table
| ID | Title | Milestone | Wave | Depends on | Size (S/M/L) | Status |
Statuses: todo / in-progress / blocked / done. This table is the scheduling
source of truth; STATE.md mirrors only the current wave.

## Dependency notes
Only non-obvious edges, one line each ("T-014 → T-009: consumes OrderService
interface").

## Wave schedule
Wave 1: T-001, T-002, T-003   (disjoint file scopes, all deps satisfied)
Wave 2: ...
A wave = tasks executable in parallel. See references/orchestration.md for how
waves are run.

## Gate map
Which gate ladder level (per-task / per-milestone / pre-ship) runs at the end of
each milestone. See references/validation-shipping.md.
```

## Task pack template (`docs/tasks/T-###-<slug>.md`)

The pack is a **context pack**: the only thing a worker agent should need beyond
the files in scope. Target ≤150 lines. If a pack wants to be longer, the task is
probably two tasks.

```markdown
# T-014 — <imperative title>
Status: todo | in-progress | blocked | done      Milestone: M2   Wave: 3
Depends on: T-009, T-011

## Goal
1–3 sentences. What exists after this task that didn't before.

## Context
Only what this task needs: 3–6 bullets of background, plus precise pointers into
frozen specs ("LLD §3.2 OrderService", "HLD §6 checkout flow"). Do NOT paste
whole spec sections — pointer + the one or two critical excerpts.

## Contracts to honor
The exact signatures / endpoint shapes / schemas this task implements or
consumes. Short ones pasted verbatim; these are frozen — deviation = plan change.

## File scope
Create: <paths>
Modify: <paths>
(Exhaustive. Touching anything else is out of bounds.)

## Suggested steps
3–7 steps. A competent path, not a straitjacket.

## Acceptance criteria
- [ ] Each criterion objectively checkable
- [ ] Include negative cases and error paths, not just the happy path
- [ ] "Tests written for X, Y edge cases" is a criterion, not an afterthought

## Validation
Exact commands, in order (e.g. `make check`, then any task-specific script).

## Out of scope
Explicit non-goals that a diligent agent might otherwise wander into.

## Handoff notes   (filled by the worker on completion)
≤10 lines: what was done, surprises, anything the next tasks must know.
```

## Sizing the plan

- A typical small product lands around 20–60 tasks across 3–6 milestones. If
  Phase 3 produces 200 tasks, the granularity is too fine (packs will outweigh
  the code); if it produces 8, they're epics, not tasks — re-split.
- Write packs for the **first two waves in full detail** and progressively refine
  later waves' packs at each milestone boundary (details learned during M0/M1
  routinely improve later packs — don't pay for detail that will be rewritten).
  The task table, however, is complete from day one.

## The approval gate (hard gate — rule 1)

Present to the user: milestone list, task count per milestone, the wave schedule,
the top 3 risks carried from the HLD, and where to browse packs. Then ask
literally: *"Reply 'approved' to begin implementation, or tell me what to
change."* Record the approval (date + scope) in `STATE.md`. Only then does
Phase 4 begin.
