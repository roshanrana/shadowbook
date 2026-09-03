# SHADOWBOOK -- single entry point. `make check` is THE definition of done
# (CLAUDE.md). CI is a thin wrapper around it and adds no step you cannot run
# here. Everything below must pass with the network disabled (NFR-8).

.DEFAULT_GOAL := help
SHELL := /bin/bash
GO ?= go
UV ?= uv

# Integration tests need a PostgreSQL. `make up` provides one; CI provides one
# as a service. When unset, integration tests skip loudly rather than fail.
LEDGER_DSN ?= postgres://shadowbook:shadowbook@localhost:5433/ledger?sslmode=disable
RECON_DSN  ?= postgres://shadowbook:shadowbook@localhost:5434/recon?sslmode=disable

help: ## List targets
	@grep -E "^[a-zA-Z_-]+:.*?## " $(MAKEFILE_LIST) | awk -F":.*?## " "{printf \"  %-16s %s\n\", \$$1, \$$2}"

## ---------------------------------------------------------------- the gate

check: fmt-check lint typecheck test-unit test-integration test-race ## THE definition of done.
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

# TODO(T-053): enforce NFR-7 coverage thresholds here once there is code to cover
# (ledger >= 85%, posting path >= 95%, reconcile classification >= 90%,
#  legacy-sim >= 85% per D-011).
coverage: ## Report coverage (not yet a gate -- see T-053)
	@$(GO) test -coverprofile=coverage.out ./... && $(GO) tool cover -func=coverage.out | tail -1
	@$(UV) run pytest --cov=legacy_sim --cov=reconcile --cov=report -m "not integration"

## ---------------------------------------------------------------- run it

demo: ## Both windows, both findings, < 5 min.
	@echo "make demo: not wired yet (M4). See STATE.md." && exit 1

up: ## Single-broker profile.
	docker compose --profile single up -d

up-chaos: ## Three-broker profile for ablation runs.
	docker compose --profile chaos up -d

down: ## Tear everything down, volumes included.
	docker compose --profile single --profile chaos down -v

ablate: ## Ablation A-C (D at M6b). Artefacts to reports/runs/.
	@echo "make ablate: not wired yet (M6). See STATE.md." && exit 1

report: ## Render reports/FINDINGS.md from run artefacts. Deterministic.
	@echo "make report: not wired yet (M6). See STATE.md." && exit 1

proto: ## Regenerate Go and Python from contracts/.
	@echo "make proto: not wired yet (T-007)." && exit 1

.PHONY: help check fmt fmt-check lint typecheck test-unit test-integration test-race \
        coverage demo up up-chaos down ablate report proto
