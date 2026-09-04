# SETUP — getting SHADOWBOOK running on the Ryzen box and in Claude Code

Do these in order. Thirty minutes, most of it downloads.

## 1. Machine

    # Go 1.23+  (https://go.dev/dl)          go version
    # Docker + compose v2                     docker compose version
    # uv (Python 3.12+ manager)               curl -LsSf https://astral.sh/uv/install.sh | sh
    # protoc + protoc-gen-go  (make proto / gen-check use these directly)
    #   protoc:        https://github.com/protocolbuffers/protobuf/releases
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
    # buf (OPTIONAL -- only for `buf lint` and `buf breaking` in CI)
    #                                         https://buf.build/docs/installation
    # vegeta (load) — settled at Phase 0, D-005   go install github.com/tsenart/vegeta/v12@latest
    # golangci-lint, govulncheck
    go install golang.org/x/vuln/cmd/govulncheck@latest
    # Claude Code
    npm install -g @anthropic-ai/claude-code

Sanity: **`make doctor`** reports what this machine can run and exactly what is
missing for the rest, in three tiers — `make demo` (Python only), `make check`
(the full gate), `make ablate` (Finding 2, needs Docker). Run it first; it will
tell you whether the next two steps are even necessary.

Then `docker compose --profile chaos up -d` should bring up three Redpanda nodes, two Postgres, Prometheus. If the box struggles, note it. `make down` afterwards.

## 2. Repository

    cd shadowbook
    git init -b main
    cp .env.example .env            # edit passwords; .env is gitignored
    unzip ~/path/to/enterprise-dev-lifecycle.zip -d .claude/skills/enterprise-dev-lifecycle/
    rm .claude/skills/enterprise-dev-lifecycle/README.md
    ls .claude/skills/enterprise-dev-lifecycle/     # SKILL.md + references/
    git add -A && git commit -m "Phase 0: brief, requirements draft, scaffold, skills"

Create the public repo `github.com/roshanrana/shadowbook`, then `git remote add origin … && git push -u origin main`. Push the scaffold now; a visible design-first history is part of the point.

## 3. Claude Code — connections

    cd shadowbook
    cp .mcp.json.example .mcp.json      # enables GitHits for API verification (you already have it in claude.ai)
    claude
    /mcp                                 # confirm githits shows connected; authenticate if prompted

Optional: a Postgres MCP server pointed at `localhost:5433` if you want Claude Code querying the ledger during runs. Not needed for the base scope.

## 4. Claude Code — skills, plugins, permissions

- **Skills.** Project skills live in `.claude/skills/` and load automatically: `enterprise-dev-lifecycle` (your gated process), `ledger-invariants`, `chaos-ablation`, `findings-report`. Verify with `/skills` (or ask Claude Code "which skills are available?").
- **Plugins.** From your Claude account, two are already enabled and relevant: **Engineering** (`architecture` for ADRs into `decisions.md`, `testing-strategy`, `system-design`, `code-review` before every merge) and **CockroachDB** (only for stretch S1). Nothing else needed; don't add AI-tooling plugins — the point of this project is that it is deterministic.
- **Permissions.** `.claude/settings.json` pre-approves `make`, `go`, `uv`, `ruff`, `mypy`, `pytest`, `docker compose`, and read-only/commit git. Force-push and destructive docker are denied. Adjust once; don't approve interactively every session.

## 5. First session

    claude
    > Read CLAUDE.md and STATE.md. We are at the Phase 0 gate. Present the requirements summary and the five open questions.

Answer the five questions. Say "approved" only when you mean it. Claude Code then produces the HLD (`02-hld.md`) with stack recommendations; the LLD; then the execution plan — which is the hard gate before any code is written.

Expected cadence: Phase 1–3 in one evening; M0 walking skeleton the next morning; M1–M4 (Finding 1, `make demo`) by end of weekend 1. Weekend 2 is the harness and Finding 2.

## 6. Working rules that save time

- Start every session with "read STATE.md and continue" — nothing else. The skill forbids repo crawls for a reason.
- One task per session or per parallel agent; task packs define file scope.
- If `make check` fails twice on a task, stop and read the task file — the two-strike rule is there to prevent the token sink.
- Commit with the task ID. Push at the end of each wave.
- When the interview lands: `git tag round-2` at whatever state exists. A truthful "here is exactly how far I got" beats a rushed finish.

## 7. Before the repo goes fully public

    govulncheck ./...
    uv run pip-audit
    git secrets --scan  (or gitleaks)
    grep -r "password" --include="*.yml" .     # nothing but ${VAR:-…} defaults

Add the "not a production system" line to the README if it has drifted. It is there now; keep it.
