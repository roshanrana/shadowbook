# SHADOWBOOK — single entry point. Targets are stubs until Phase 4 (guardrails)
# wires them; each stub fails loudly so nothing "passes" by accident.

.DEFAULT_GOAL := help
SHELL := /bin/bash

help: ## List targets
	@grep -E "^[a-zA-Z_-]+:.*?## " $(MAKEFILE_LIST) | awk -F":.*?## " "{printf \"  %-14s %s\n\", \$$1, \$$2}"

check: ## Format + lint + type-check + unit + integration + race. THE definition of done.
	@echo "make check: not wired yet (Phase 4). See STATE.md." && exit 1

demo: ## 30 simulated business days, both findings, < 5 min.
	@echo "make demo: not wired yet (M4). See STATE.md." && exit 1

up: ## Bring up the single-node compose profile.
	docker compose --profile single up -d

up-chaos: ## Bring up the three-broker compose profile for ablation runs.
	docker compose --profile chaos up -d

down: ## Tear everything down (volumes included).
	docker compose --profile single --profile chaos down -v

ablate: ## Run ablation A–D (≥3 runs each) and write artefacts to reports/runs/.
	@echo "make ablate: not wired yet (M6). See STATE.md." && exit 1

report: ## Render reports/FINDINGS.md from run artefacts. Deterministic.
	@echo "make report: not wired yet (M6). See STATE.md." && exit 1

proto: ## Regenerate Go and Python code from contracts/.
	@echo "make proto: not wired yet (Phase 4)." && exit 1

.PHONY: help check demo up up-chaos down ablate report proto
