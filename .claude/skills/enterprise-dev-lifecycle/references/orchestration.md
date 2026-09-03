# Agent Orchestration — Roles, Waves, Verification

Read this once at the start of Phase 5. It applies whether "multiple agents"
means parallel sessions/cloud tasks, sub-agents, or one session playing roles in
sequence — the coordination rules are identical because all coordination happens
through files, never through shared chat memory.

## The three roles

**Orchestrator** — owns the plan. Reads `STATE.md` + the execution plan's task
table; assigns/schedules tasks; writes and refreshes task packs; merges completed
work; runs milestone gates; updates `STATE.md` and the task table; talks to the
user at gates. The orchestrator does not implement.

**Worker** — owns one task. Reads its pack + in-scope files; implements within
scope; runs the task's validation; fills Handoff notes. A worker never edits the
plan, other packs, or files outside its scope. If reality contradicts the pack,
it stops and reports (pack defect) rather than improvising.

**Verifier** — owns judgment on one task. Reads ONLY the diff + the pack's
acceptance criteria (deliberately *not* the worker's conversation — fresh eyes
are the point); checks each criterion plus the standing review list below; writes
pass/fail per criterion into the pack. The verifier never fixes code — findings
go back to the worker. Separating "wrote it" from "judged it" catches the
self-consistency failures that make agents grade their own homework kindly.

Standing verifier checklist (beyond the pack's criteria): scope respected; frozen
contracts honored exactly; error paths handled per the LLD error taxonomy; no
secrets/PII in code or logs; tests actually assert behavior (not tautologies);
no drive-by refactors.

## Single-session mode (default in a CLI)

One session cycles roles explicitly per task: orchestrator hat (pick task, confirm
pack fresh) → worker hat (implement) → **verifier hat with a clean slate**: re-read
only the diff and the criteria, deliberately setting aside the implementation
reasoning, and judge as if seeing it cold. Then gates, handoff, next task.
Announce hat switches in one line — it audibly prevents role-blur.

## Parallel mode (multiple sessions / cloud tasks / sub-agents)

- **Schedule by waves** from the execution plan. A wave's tasks have all
  dependencies satisfied and pairwise-disjoint file scopes, so they cannot
  conflict. The orchestrator dispatches a wave, collects results, merges, runs
  the milestone-appropriate gates, updates state, then opens the next wave.
- **One writer per file, ever.** If two ready tasks overlap on a file, they are
  serialized or the plan is re-split — never run concurrently.
- **Dispatch = the pack.** A worker is launched with: the task pack path, the
  repo, and nothing else. If a pack isn't sufficient to launch from, fix the pack
  (that defect would have cost tokens in every future session too).
- **Contract-first parallelism.** Waves may parallelize *because* LLD §4 froze the
  interfaces. A worker needing an interface change stops; the orchestrator treats
  it as an LLD change (SKILL.md "when things go wrong") before any dependent work
  continues.
- **Merge order:** merge a wave task-by-task, running fast gates after each merge,
  so a breakage is attributable to one task instead of a wave-sized bisect.

## Right-sizing effort (tokens follow difficulty)

- Reserve maximum model capability / reasoning effort for: Phases 0–3, verifier
  passes on critical-path tasks, and debugging after a first gate failure.
- Mechanical work (scaffold from a spec, boilerplate, config, test fixtures) runs
  fine at lower effort/cheaper settings when the environment offers a choice.
- Never parallelize for its own sake: dispatch overhead + merge cost exceeds the
  gain for small waves. Parallelism pays on waves of ≥3 medium tasks; otherwise
  run sequentially.

## Failure handling

- **Two-strike rule (SKILL.md rule 6) is per worker per task.** Two failed gate
  attempts → stop, write findings + hypotheses into the pack, mark `blocked`,
  return control to the orchestrator. The orchestrator may: re-scope the task,
  fix the pack, schedule a spike, or escalate to the user. It does not simply
  relaunch the same worker at the same task — that's how token-burning loops start.
- **A worker that goes silent or overruns badly** is treated as failed: discard
  its partial diff unless gates pass on it; never merge unverified partial work.
- **Repeated failures across different tasks in one area** signal a design defect,
  not worker error — the orchestrator raises it as an LLD issue at the next gate.

## What the orchestrator reports to the user

At each wave/milestone boundary, a compact digest (not a transcript): tasks
completed with one-line outcomes, gate results, deviations recorded, blockers +
batched questions, and what the next wave contains. The user steers the project
through these digests and the gate decisions — they should never need to read
diffs to know where things stand.
