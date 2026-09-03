# T-002 — Toolchain config and the single `make check`
Status: todo      Milestone: M-G   Wave: 2
Depends on: T-001

## Goal
`make check` exists, runs format + lint + type-check + vet + unit + integration + `-race` across both toolchains, and passes on the empty tree from T-001. It is from this point the only definition of done.

## Context
- CLAUDE.md: "**`make check` is the only definition of done.**" CI is a thin wrapper (T-003), never a divergent pipeline.
- NFR-6: `make check` under three minutes locally and identical in CI. NFR-8: it must pass **with the network disabled**.
- The existing `Makefile` has stub targets that `exit 1` on purpose. This task replaces the `check` stub only; `demo`, `ablate`, `report` and `proto` stay stubs.
- Coverage thresholds are NFR-7 + D-011 but are **not** enforced until T-053 — an empty tree cannot hit them. Wire the flags, leave the gate off, and leave a `TODO(T-053)`.

## Contracts to honor
LLD §2 conventions table: `gofmt` + `golangci-lint`; `ruff format` + `ruff check` + `mypy --strict`; `pytest`. Configs at repo root, one per language.

## File scope
Create: `.golangci.yml`, `ruff.toml` (or `[tool.ruff]` in the root `pyproject.toml` — pick one and say which in handoff), `mypy.ini` (or `[tool.mypy]`), `.pre-commit-config.yaml`.
Modify: `Makefile` (the `check` target and any helper targets it needs), root `pyproject.toml` (dev dependency group only).

## Suggested steps
1. Write `check` as a sequence of named sub-targets (`fmt-check`, `lint`, `typecheck`, `test-unit`, `test-integration`, `test-race`) so a failure names itself.
2. `golangci-lint` with `errcheck`, `govet`, `staticcheck`, `ineffassign`, `gosec` enabled.
3. `mypy --strict` over all three Python members; `ruff` with an explicit rule set, not defaults-by-accident.
4. Add `test-race` running `go test -race ./...`.
5. Verify offline: `unshare -rn make check` or equivalent — it must not reach the network.
6. Time it and record the number in handoff notes.

## Acceptance criteria
- [ ] `make check` exits 0 on the T-001 tree
- [ ] `make check` exits **non-zero** if a deliberately malformed `.go` or `.py` file is added (test this, then revert)
- [ ] Each sub-target can be run alone and names itself on failure
- [ ] `make check` passes with no network access
- [ ] Wall time recorded in handoff notes; if over 90 s on an empty tree, say so — it will only grow
- [ ] `make demo`, `make ablate`, `make report`, `make proto` still exit 1 with their existing messages

## Validation
```
make check
make demo || true    # must still fail loudly
```

## Out of scope
CI (T-003). Coverage enforcement (T-053). Any application code. Changing the `up`/`down`/`up-chaos` targets (T-004).

## Handoff notes
_(filled by the worker)_
