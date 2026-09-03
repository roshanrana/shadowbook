# T-003 — CI workflow as a thin wrapper around `make check`
Status: todo      Milestone: M-G   Wave: 2
Depends on: T-001

## Goal
GitHub Actions runs exactly `make check` on push and pull request, on a runner whose toolchain versions match the ones SETUP.md pins. CI adds no step that a developer cannot run locally.

## Context
- CLAUDE.md: "One `make check`; CI is a thin wrapper around it." A CI file that lints differently from local is the failure mode this rule exists to prevent.
- `.github/workflows/check.yml` already exists from the Phase 0 scaffold — read it before rewriting; it may already be close.
- NFR-8 requires the offline property. CI *may* have network for toolchain setup, but the `make check` step itself must not need it.
- Two toolchains to install: Go 1.23+ and uv (Python 3.12+). Cache both.

## Contracts to honor
The job's only build step is `make check`. Any additional step must be setup, caching, or artefact upload — never a check that could disagree with local.

## File scope
Modify: `.github/workflows/check.yml`
Out of bounds: `Makefile`, all config files from T-002.

## Suggested steps
1. Read the existing workflow first.
2. Triggers: `push` to `main`, `pull_request`.
3. `actions/setup-go` with the version from `go.mod`; `astral-sh/setup-uv` with caching.
4. One run step: `make check`.
5. Add a second job that runs `make check` with networking disabled, or document why that cannot be expressed on the runner.

## Acceptance criteria
- [ ] The workflow's only verification step is `make check`
- [ ] Go version is read from `go.mod`, not hardcoded in two places
- [ ] Both toolchain caches are configured
- [ ] Workflow is valid YAML and parses (`actionlint` or equivalent)
- [ ] No secret, token or credential appears in the file

## Validation
```
actionlint .github/workflows/check.yml   # or: python -c "import yaml,sys; yaml.safe_load(open(sys.argv[1]))" …
make check
```

## Out of scope
Release, publish, or deploy workflows. Coverage reporting to a third-party service. Branch protection settings (owner does that in the GitHub UI).

## Handoff notes
_(filled by the worker)_
