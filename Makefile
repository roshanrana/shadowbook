# SHADOWBOOK -- single entry point. `make check` is THE definition of done
# (CLAUDE.md). CI is a thin wrapper around it and adds no step you cannot run
# here. Everything below must pass with the network disabled (NFR-8).

.DEFAULT_GOAL := help
SHELL := /bin/bash
GO ?= go
UV ?= uv

# Integration tests need a PostgreSQL. `make up` provides one; CI provides one
# as a service. When unset, integration tests skip loudly rather than fail.
RUN_DIR ?= reports/runs/demo
LEDGER_DSN ?= postgres://shadowbook:shadowbook@localhost:5433/ledger?sslmode=disable
RECON_DSN  ?= postgres://shadowbook:shadowbook@localhost:5434/recon?sslmode=disable

help: ## List targets
	@grep -E "^[a-zA-Z_-]+:.*?## " $(MAKEFILE_LIST) | awk -F":.*?## " "{printf \"  %-16s %s\n\", \$$1, \$$2}"

## ---------------------------------------------------------------- the gate

check: fmt-check lint typecheck gen-check golden-check test-unit test-integration test-race ## THE definition of done.
	@echo "make check: PASS"

fmt-check: ## gofmt + ruff format, non-mutating
	@out=$$(gofmt -l $$(find . -name '*.go' -not -path './gen/*')); \
	 if [ -n "$$out" ]; then echo "gofmt: these files need formatting:"; echo "$$out"; exit 1; fi
	@$(UV) run ruff format --check . || { echo "ruff format: run 'make fmt'"; exit 1; }

fmt: ## Rewrite formatting in place
	@gofmt -w $$(find . -name '*.go' -not -path './gen/*')
	@$(UV) run ruff format .

lint: ## golangci-lint + ruff check
	@golangci-lint run ./... || { echo "lint: FAILED"; exit 1; }
	@$(UV) run ruff check . || { echo "ruff: FAILED"; exit 1; }

typecheck: ## go vet + mypy --strict
	@$(GO) vet ./... || { echo "go vet: FAILED"; exit 1; }
	@$(UV) run mypy --config-file mypy.ini legacy-sim/src reconcile/src report/src \
		|| { echo "mypy: FAILED"; exit 1; }

test-unit: ## Go + Python unit tests
	@$(GO) test ./... || { echo "go test: FAILED"; exit 1; }
	@$(UV) run pytest -m "not integration"; rc=$$?; \
	 if [ $$rc -ne 0 ] && [ $$rc -ne 5 ]; then echo "pytest: FAILED"; exit 1; fi

test-integration: ## Tests needing a live PostgreSQL; skip loudly when absent
	@SHADOWBOOK_LEDGER_DSN="$(LEDGER_DSN)" SHADOWBOOK_RECON_DSN="$(RECON_DSN)" \
		$(GO) test -tags=integration ./... || { echo "go integration: FAILED"; exit 1; }
	@SHADOWBOOK_LEDGER_DSN="$(LEDGER_DSN)" SHADOWBOOK_RECON_DSN="$(RECON_DSN)" \
		$(UV) run pytest -m integration; rc=$$?; \
	 if [ $$rc -ne 0 ] && [ $$rc -ne 5 ]; then echo "pytest integration: FAILED"; exit 1; fi

test-race: ## go test -race
	@$(GO) test -race ./... || { echo "go test -race: FAILED"; exit 1; }

coverage: ## NFR-7 gate: ledger >= 85%, posting path >= 95%, reconcile >= 90%, legacy-sim >= 85%
	@scripts/coverage.sh
	@$(UV) run pytest -q -m "not integration" --cov=reconcile --cov-fail-under=90 \
		--cov-report=term:skip-covered > /dev/null \
		|| { echo "coverage: reconcile below 90% (NFR-7)"; exit 1; }
	@$(UV) run pytest -q -m "not integration" --cov=legacy_sim --cov-fail-under=85 \
		--cov-report=term:skip-covered > /dev/null \
		|| { echo "coverage: legacy-sim below 85% (D-011)"; exit 1; }
	@echo "coverage: all NFR-7 targets met"

## ---------------------------------------------------------------- run it

demo: ## Both windows, Finding 1, < 5 min. Regenerates reports/FINDINGS.md.
	@scripts/demo.sh

up: ## Single-broker profile.
	docker compose --profile single up -d

up-chaos: ## Three-broker profile for ablation runs.
	docker compose --profile chaos up -d

down: ## Tear everything down, volumes included.
	docker compose --profile single --profile chaos down -v

ablate: ## Ablation A-C (D at M6b). Artefacts to reports/runs/.
	@$(GO) run ./cmd/harness ablate --out $(RUN_DIR) --runs 3

preflight: ## Report whether an ablation could run here, and why not if not.
	@$(GO) run ./cmd/harness preflight

report: ## Render reports/FINDINGS.md from run artefacts. Deterministic.
	@$(UV) run python -m report.render \
		--finding1 $(RUN_DIR)/finding1.json \
		--finding2 $(RUN_DIR)/finding2.json \
		--repo . --out reports/FINDINGS.md

proto: ## Regenerate Go and Python from contracts/. Output is committed.
	@cd contracts && protoc --go_out=../gen/go --go_opt=paths=source_relative \
		--python_out=../gen/python --pyi_out=../gen/python \
		-I . $$(find . -name '*.proto' | sort)
	@echo "make proto: regenerated. Commit gen/ if it changed."

golden-calendar: ## Regenerate the Go->Python calendar golden file
	@$(GO) run ./cmd/goldencal > legacy-sim/tests/golden/calendar.json
	@echo "regenerated legacy-sim/tests/golden/calendar.json"

golden-check: ## Fail if the calendar golden file is stale
	@$(GO) run ./cmd/goldencal > /tmp/calendar.golden.json
	@diff -q /tmp/calendar.golden.json legacy-sim/tests/golden/calendar.json >/dev/null \
		|| { echo "calendar golden is stale: run 'make golden-calendar'"; exit 1; }

gen-check: ## Fail if gen/ is stale relative to contracts/ (needs protoc)
	@if command -v protoc >/dev/null; then scripts/check-gen-diff.sh; \
	 else echo "gen-check: protoc absent, skipping (CI enforces it)"; fi

.PHONY: help check coverage fmt fmt-check lint typecheck gen-check golden-check golden-calendar preflight test-unit test-integration test-race \
        coverage demo up up-chaos down ablate report proto
