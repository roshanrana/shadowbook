# Validation & Shipping — Guardrails, Gate Ladder, Ship Report

Read the Guardrails section when entering Phase 4; the rest when entering
Phases 6–7. The principle throughout: validation is automated, layered, and
identical everywhere (local, per-task, CI), so "it works" is always a claim
backed by the same reproducible command.

## Guardrails before features (Phase 4)

Set up, in order, before any feature code:

1. **Scaffold** the repo exactly per LLD §1 (layout) and §2 (conventions).
2. **Toolchain pinning:** language version file, lockfiles, `.editorconfig`.
3. **Formatter + linter** with the strictest sane preset; auto-fix wired in.
4. **Type checking** at the strictest level the stack supports from day one —
   retrofitting strictness onto a grown codebase costs 10× more.
5. **Test harness** with one example unit test and one example integration test
   running (proves wiring, gives every future task a pattern to copy).
6. **Secrets hygiene:** `.env.example` (never `.env`), secrets scanner config,
   `.gitignore` done properly.
7. **The `check` command** (below) and **pre-commit hooks** running its fast subset.
8. **CI pipeline** that runs `make check` on every push — CI must be a mirror of
   the local command, never a superset that lets "passes locally" and "passes CI"
   diverge.
9. **Walking skeleton (M0)** built and pushed through this pipeline to the target
   environment.

## The single check command

One entry point, whatever the stack (`make check` / `npm run check` / `just
check` / a `check.sh`). Composition:

```
check      = format-check + lint + typecheck + unit tests + build        (fast, <2–3 min)
check-full = check + integration tests + e2e smoke + coverage report
check-ship = check-full + dependency audit + SAST + secrets scan + licenses + perf smoke
```

Every task pack's Validation section starts with `make check`. Agents, humans,
pre-commit, and CI all speak this one vocabulary — nobody maintains parallel
lists of "the real checks."

## Gate ladder

| Level | When | Contents |
|---|---|---|
| Per-task | every task, before `done` | `check` + task-specific commands from the pack |
| Per-wave | orchestrator, after merging each task of a wave | `check` (fast, attribution-friendly) |
| Per-milestone | end of each milestone | `check-full`; walking-skeleton path re-verified; migrations run clean on a fresh DB |
| Pre-ship (Phase 6) | before the ship report | `check-ship` + the hardening list below |

## Definition of Done (per task)

A task is `done` only when: acceptance criteria all pass; validation commands
green; new logic covered by tests written in the same task (never "tests later");
no lint/type suppressions added without a one-line justification comment; docs
touched if behavior/config changed; Handoff notes filled; `STATE.md` log line
appended; committed with the task ID.

## Hardening pass (Phase 6)

Run and record results for each — findings become normal tasks with packs:

- Dependency audit (known CVEs) and license check against an allowlist.
- Static security scan (SAST) + secrets scan over history, not just HEAD.
- AuthZ probe: every endpoint exercised as wrong-role and as unauthenticated.
- Input abuse: oversized payloads, malformed encodings, injection strings on
  every external input surface.
- Coverage vs the LLD §6 target on core logic (don't chase 100%; chase the target).
- Performance smoke against the NFR budgets (k6/Locust/etc. — a smoke, not a
  full load test, unless requirements demand one).
- Failure drills appropriate to the stack: dependency down, DB restart,
  slow upstream — verify the degrade behavior the HLD promised.
- Observability check: logs/metrics/traces actually emitted and PII-free;
  health endpoints accurate.

## Ship report template (`docs/ship-report.md`)

```markdown
# Ship Report — <project> <version>
Date • Commit • Environments covered

## Gate evidence
check-ship output summary; link/attach full logs. Coverage: X% (target Y%).

## Security
Audit results, SAST findings + resolutions, secrets scan clean, authz probe notes.

## Performance
NFR budget vs measured, one line per budget.

## Deployment
Exact steps (or pipeline link), env var & secrets matrix (names only, no values),
migration plan for this release.

## Rollback plan
Trigger criteria, exact steps, data considerations. If rollback is impossible
for some step, say so in bold with the mitigation.

## Observability
Dashboards/alerts in place; the one query/dashboard to watch during rollout.

## Known issues & deferred items
Honest list, each with severity and a tracked follow-up task ID.

## Go / No-Go
Recommendation + what would change it.
```

Present the report; the user makes the call. If no-go, findings become tasks and
the loop continues — the report is re-issued, not patched.

## Documentation minimums for shipping

`README.md` (what it is, how to run locally in ≤5 commands, how to run `check`),
a runbook (start/stop, common failures, where logs live), API reference generated
from the contracts (OpenAPI or equivalent — generated, so it can't drift), and
`decisions.md` up to date. Documentation lags reality = not done.
