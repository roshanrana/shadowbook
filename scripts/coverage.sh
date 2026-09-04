#!/usr/bin/env bash
# NFR-7 coverage gate (T-053), amended by D-011.
#
# Coverage is enforced per GROUP, not per package, because the targets in the
# requirements are about paths through the system rather than about files:
#   ledger        >= 85%   the whole ledger, its store and its invariants
#   posting path  >= 95%   the write path and the two value types it rests on
#   reconcile     >= 90%   classification and the grains
#   legacy-sim    >= 85%   determinism depends on it (D-011)
#
# Integration tests are included: most of the ledger's behaviour is only
# reachable through a real database, so a unit-only number would flatter it.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${SHADOWBOOK_LEDGER_DSN:?coverage needs a database; run make up first}"

PROFILE="${PROFILE:-coverage.out}"
go test -tags=integration -count=1 \
    -coverprofile="${PROFILE}" \
    -coverpkg=./internal/...,./migrations/... ./... > /dev/null

go tool cover -func="${PROFILE}" | python3 scripts/coverage.py
