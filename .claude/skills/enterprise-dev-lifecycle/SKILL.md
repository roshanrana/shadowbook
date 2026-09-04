---
name: enterprise-dev-lifecycle
description: Gated, document-driven lifecycle for building enterprise-grade software with AI agents — requirements, HLD, LLD, tech-stack recommendations, and a task-level execution plan with an explicit human approval gate, followed by guardrails-first implementation, automated validation, and a ship-readiness report. Use this skill whenever the user wants to build an application, service, API, platform, or system from scratch, start a greenfield project or a major new feature, or asks for architecture design, HLD/LLD documents, a tech-stack recommendation, or a project execution plan — even if they only say "build me X" without mentioning process. Also use it to resume any project whose repo contains a STATE.md produced by this skill.
---

# Enterprise Development Lifecycle

Run a phased, gated development lifecycle: design everything up front in reviewable
documents, get explicit human approval, then implement in small, independently
validated tasks. All project knowledge lives in files — never in chat memory — so
every session (and every parallel agent) starts cheap and stays in scope.

Two goals drive everything in this skill:

1. **Quality through gates.** No phase begins until the previous phase's output is
   validated (by the human at design gates, by automated checks at build gates).
2. **Token efficiency through frozen context.** Expensive thinking (architecture,
   planning) happens once and is written down. Implementation agents read small,
   pre-built context packs instead of re-deriving decisions or crawling the repo.

## Non-negotiable rules

1. **Never write application code before the execution plan is approved.** The user
   must reply with an explicit "approved" (or equivalent unambiguous confirmation)
   at the Phase 3 gate. "Looks good, but…" is not approval — resolve the "but" first.
2. **Present every design gate as a decision.** End each design phase with: the
   document location, a ≤10-line summary, any open questions (numbered), and the
   literal question: *"Reply 'approved' to proceed, or tell me what to change."*
3. **All state lives in files.** `STATE.md`, the design docs, and task packs are the
   single source of truth. Assume chat history will be lost at any moment.
4. **One command validates everything.** From Phase 4 onward, `make check` (or the
   ecosystem equivalent) must exist and pass before any task is marked done. CI runs
   the same command.
5. **Stay in scope.** A task may only touch files listed in its task pack. If the
   task can't be completed without touching other files, stop, update the plan, and
   note the deviation in `STATE.md` — don't improvise.
6. **Two-strike rule.** If a task fails its validation gate twice, stop working on
   it. Write findings into the task file, mark it `blocked`, and escalate to the
   user (or the orchestrator). Thrashing loops are the single biggest token sink.

## Artifacts this skill creates and maintains

| File | Purpose | Created in |
|---|---|---|
| `docs/design/01-requirements.md` | Functional + non-functional requirements, constraints, out-of-scope | Phase 0 |
| `docs/design/02-hld.md` | Architecture, components, data flow, **tech-stack recommendation** | Phase 1 |
| `docs/design/03-lld.md` | Contracts, schemas, module design, error taxonomy, test plan | Phase 2 |
| `docs/design/04-execution-plan.md` | Milestones, task table, dependency graph, wave schedule | Phase 3 |
| `docs/design/decisions.md` | Append-only decision log (mini-ADRs) | Phase 1+ |
| `docs/tasks/T-###-<slug>.md` | One context pack per task — the only context a worker needs | Phase 3 |
| `STATE.md` (repo root) | Living ledger: phase, current tasks, task log, blockers, deviations | Phase 0 |
| `docs/ship-report.md` | Evidence of readiness: gates, coverage, audits, rollback plan | Phase 7 |

## The phase pipeline

Work through the phases in order. Each phase names the reference file to read
**when you reach it** — do not pre-load references for later phases.

### Phase 0 — Intake & requirements
Run intake with the user about: what the system does, who uses it, scale expectations,
performance/availability targets, security & compliance context (PII? regulated
industry?), integration points, budget/hosting constraints, team skills, and hard
deadlines. Ask in **one batched set of numbered questions** — not a drip-feed.
Write `docs/design/01-requirements.md` (functional requirements, non-functional
requirements with measurable targets, constraints, assumptions, explicit
out-of-scope list). Create `STATE.md`. **Gate:** user confirms requirements.

### Phase 1 — High-level design (HLD)
Read `references/design-templates.md`, then produce `docs/design/02-hld.md`:
architecture style with rationale, system context and component breakdown, data
architecture, 3–5 critical flows, cross-cutting concerns, non-functional design,
risks. Include the **tech-stack recommendation**: for each layer, 2–3 viable
options with one-line trade-offs and a bolded recommendation justified against
*this* project's requirements and the team's skills. **Gate:** user approves the
HLD and confirms or overrides each stack recommendation.

### Phase 2 — Low-level design (LLD)
Read `references/design-templates.md` (LLD section), then produce
`docs/design/03-lld.md`: repo layout, per-component module design, exact API
contracts, data schemas/DDL, sequence detail for critical flows, error taxonomy,
config matrix, and the test strategy mapped to components. Interfaces defined here
are **frozen contracts** — parallel work in later phases depends on them not
drifting. **Gate:** user approves the LLD.

### Phase 3 — Execution plan
Read `references/execution-planning.md`, then produce
`docs/design/04-execution-plan.md` and one task pack per task in `docs/tasks/`.
Decompose milestones into atomic tasks (rules in the reference), build the
dependency graph, schedule parallelizable waves, and map validation gates to
milestones. Milestone M0 is always the **walking skeleton** (thinnest end-to-end
slice through the real architecture and pipeline). **Gate:** this is the hard
gate — present the plan summary and require the explicit "approved" per rule 1.

### Phase 4 — Guardrails before features
Read `references/validation-shipping.md` (guardrails section). Before any feature
code: scaffold the repo per the LLD layout, set up formatter, linter, type
checking, test harness, pre-commit hooks, the single `check` command, and a CI
pipeline that runs it. Then build M0 (walking skeleton) through that pipeline.
Everything built afterward is validated automatically for free. **Gate:**
`make check` green + walking skeleton runs end to end.

### Phase 5 — Implementation loop
Read `references/context-engineering.md` and `references/orchestration.md` once,
at the start of this phase. Then, per task:

1. Read `STATE.md` → identify the next unblocked task in the current wave.
2. Load **only** that task's pack and the files in its scope.
3. Implement within scope; keep diffs minimal.
4. Self-review the diff against the acceptance criteria (verifier hat — see
   orchestration reference).
5. Run the task's validation commands. Fix and re-run (two-strike rule applies).
6. Fill the task pack's Handoff notes, append one line to the `STATE.md` task log,
   update "Now / next", commit with the task ID in the message.

When running multiple agents or parallel sessions, the orchestration reference
governs scheduling, file-scope isolation, and handoffs.

### Phase 6 — Validation & hardening
Read `references/validation-shipping.md` (gate ladder). Run the milestone- and
ship-level gates: integration and e2e suites, dependency audit, static security
scan, secrets scan, coverage against targets, performance smoke against the
budgets set in requirements. Fix findings as tasks (create packs; they follow the
same loop).

### Phase 7 — Ship readiness
Produce `docs/ship-report.md` from the template in
`references/validation-shipping.md`: gate evidence, coverage, audit results,
deployment steps, environment/secrets matrix, observability confirmation,
rollback plan, known issues. Ensure README, runbook, and API docs exist and match
reality. **Gate:** present the ship report; the user makes the go/no-go call.

## Session-start protocol (every session, including after interruptions)

1. Check for `STATE.md` in the repo root.
2. **If present:** read it, read `docs/design/04-execution-plan.md`'s task table
   (not the whole document), and resume at the recorded phase/task. Do not re-read
   requirements, HLD, or LLD unless the current task pack points into them.
3. **If absent:** you are at Phase 0. Start the intake session.
4. Never begin a session with a repo-wide crawl, `git log` archaeology, or
   re-summarizing the project — `STATE.md` exists precisely so you don't have to.

## Token discipline (always on)

The full ruleset is in `references/context-engineering.md`. The five that matter
most, active at all times:

- **Read narrow:** `STATE.md` → task pack → in-scope files. Nothing else by default.
- **Specs are frozen:** cite `03-lld.md §x` instead of re-deriving or re-debating
  decisions. Re-opening a decision is a plan change, not a mid-task tangent.
- **Write small:** targeted edits over file rewrites; never echo file contents back
  into the conversation; keep handoff notes ≤10 lines.
- **Batch questions:** questions go to gates. Mid-task blockers get written to the
  task file, the task gets marked `blocked`, and you move to the next unblocked task.
- **Scripts over reasoning:** deterministic work (checks, codegen, migrations,
  fixtures) belongs in committed scripts run by the `check` command, not in
  ad-hoc reasoning each session.

## When things go wrong

- **Gate fails twice** → two-strike rule (rule 6): stop, document, escalate.
- **Scope doesn't fit** → stop; propose a plan amendment (split the task, adjust
  file scope, or add a task); record it in `decisions.md`; material changes to
  approved designs need user sign-off.
- **A frozen interface must change** → that is an LLD change: update `03-lld.md`,
  list every task pack affected, get approval, then propagate.
- **The user asks for something outside the plan mid-build** → capture it as a new
  task or milestone in the plan first (30 seconds of bookkeeping), then decide with
  the user whether it enters the current wave or the backlog.
