# T-001 — Scaffold the repository layout
Status: todo      Milestone: M-G   Wave: 1
Depends on: —

## Goal
Every directory and placeholder file of LLD §1 exists, `go build ./...` and `uv sync` both succeed on an empty tree, and nothing else has been invented. After this task the repo has a shape; it has no behaviour.

## Context
- LLD §1 is an **exact spec, not a sketch**. Directories not listed there are not created. CLAUDE.md forbade service directories before Phase 2 precisely so this task could be unambiguous.
- Go module path is `github.com/roshanrana/shadowbook` (D-009). Go 1.23+.
- Python is a **uv workspace with three members** — `legacy-sim`, `reconcile`, `report` — not one package. Only `report` depends on Jinja2 (D-013).
- `legacy-sim/quirks.yaml` already exists at that path and must not be moved.
- Placeholder Go packages need one file with a package clause so `go build ./...` passes; placeholder Python packages need `__init__.py`.

## Contracts to honor
The directory tree in LLD §1, verbatim. Module path `github.com/roshanrana/shadowbook`.

## File scope
Create: `go.mod`, `pyproject.toml` (workspace root), `legacy-sim/pyproject.toml`, `reconcile/pyproject.toml`, `report/pyproject.toml`, and one placeholder source file in each package directory named in LLD §1 (`internal/**`, `cmd/**`, `legacy-sim/src/legacy_sim/**`, `reconcile/src/reconcile/**`, `report/src/report/**`), plus empty `migrations/.gitkeep`, `contracts/.gitkeep`, `gen/.gitkeep`, `reports/runs/.gitkeep`.
Modify: `.gitignore` (add `gen/**/__pycache__`, `.venv`, `*.test`).
Out of bounds: `Makefile`, `docker-compose.yml`, `.github/`, anything in `docs/`.

## Suggested steps
1. `go mod init github.com/roshanrana/shadowbook`; set `go 1.23`.
2. Create every `internal/` and `cmd/` directory from LLD §1 with a single `.go` file containing only its package clause.
3. Write the root `pyproject.toml` with `[tool.uv.workspace] members = ["legacy-sim", "reconcile", "report"]`.
4. Write the three member `pyproject.toml` files with `requires-python = ">=3.12"` and `src/` layout; Jinja2 only in `report`.
5. `go build ./...` and `uv sync` must both succeed.
6. `git status` must show no file outside the scope above.

## Acceptance criteria
- [ ] `go build ./...` exits 0
- [ ] `uv sync` exits 0 and creates one lockfile at the repo root, not three
- [ ] Every directory in LLD §1 exists; `find . -type d` shows no directory absent from LLD §1
- [ ] `legacy-sim/quirks.yaml` is unmoved and unmodified
- [ ] No `.go` file contains a function; no `.py` file contains a class or def
- [ ] Jinja2 appears in `report/pyproject.toml` only

## Validation
```
go build ./...
uv sync
git status --porcelain
```

## Out of scope
Any lint, type-check or test configuration (T-002). Any CI (T-003). Any compose change (T-004). Writing real code of any kind.

## Handoff notes
_(filled by the worker)_
