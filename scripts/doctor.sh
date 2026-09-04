#!/usr/bin/env bash
# make doctor -- what this machine can and cannot run, and what to install.
#
# Three tiers, because they need very different things:
#   demo   Finding 1. Python only. No Go, no Docker.
#   check  the full gate. Go toolchain, protoc, golangci-lint, PostgreSQL.
#   ablate Finding 2. Everything above plus a Docker daemon.
set -uo pipefail
cd "$(dirname "$0")/.."

have() { command -v "$1" >/dev/null 2>&1; }
row()  { printf "  %-18s %-8s %s\n" "$1" "$2" "${3:-}"; }

demo_ok=1; check_ok=1; ablate_ok=1

echo "SHADOWBOOK doctor"
echo
echo "TIER 1 -- make demo (Finding 1)"
if have uv; then row "uv" "ok" "$(uv --version 2>/dev/null)"; else
  row "uv" "MISSING" "curl -LsSf https://astral.sh/uv/install.sh | sh"; demo_ok=0; fi
if [ -f uv.lock ]; then row "uv.lock" "ok"; else row "uv.lock" "MISSING" "run: uv sync"; demo_ok=0; fi

echo
echo "TIER 2 -- make check (the gate)"
if have go; then
  gov=$(go version | awk '{print $3}')
  row "go" "ok" "$gov (go.mod needs $(grep -m1 '^go ' go.mod | awk '{print $2}')+)"
else
  row "go" "MISSING" "https://go.dev/dl"; check_ok=0
fi
if [ -f go.sum ]; then
  row "go.sum" "ok"
else
  row "go.sum" "MISSING" "run: go mod tidy   (see decisions.md D-016 -- REQUIRED)"; check_ok=0
fi
if [ -f go.work ]; then
  row "go.work" "REMOVE" "sandbox-only workaround; delete it on a normal machine (D-016)"; check_ok=0
else
  row "go.work" "ok" "absent, as it should be"
fi
if have golangci-lint; then row "golangci-lint" "ok" "$(golangci-lint --version 2>/dev/null | head -1 | cut -c1-40)"; else
  row "golangci-lint" "MISSING" "https://golangci-lint.run/welcome/install"; check_ok=0; fi
if have protoc; then row "protoc" "ok" "$(protoc --version)"; else
  row "protoc" "MISSING" "needed by make proto / gen-check"; check_ok=0; fi
if have protoc-gen-go; then row "protoc-gen-go" "ok" "$(protoc-gen-go --version 2>&1 | head -1)"; else
  row "protoc-gen-go" "MISSING" "go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"; check_ok=0; fi

dsn="${SHADOWBOOK_LEDGER_DSN:-postgres://shadowbook:shadowbook@localhost:5433/ledger?sslmode=disable}"
if have psql && psql "$dsn" -tAc 'select 1' >/dev/null 2>&1; then
  row "PostgreSQL" "ok" "reachable on 5433"
else
  row "PostgreSQL" "MISSING" "run: make up   (integration tests skip without it)"; check_ok=0
fi

echo
echo "TIER 3 -- make ablate (Finding 2 numbers)"
if have docker && docker info >/dev/null 2>&1; then
  row "docker" "ok" "server $(docker info --format '{{.ServerVersion}}' 2>/dev/null)"
else
  row "docker" "MISSING" "the three-broker chaos profile needs a daemon"; ablate_ok=0
fi
row "ablation runner" "TODO" "orchestration not implemented (T-048, ship-report §4)"
ablate_ok=0

echo
[ "$demo_ok"   -eq 1 ] && echo "make demo:   READY"   || echo "make demo:   blocked -- see TIER 1"
[ "$check_ok"  -eq 1 ] && echo "make check:  READY"   || echo "make check:  blocked -- see TIER 2"
[ "$ablate_ok" -eq 1 ] && echo "make ablate: READY"   || echo "make ablate: blocked -- see TIER 3"
echo
echo "docs/ship-report.md is the honest account of what is and is not measured."
