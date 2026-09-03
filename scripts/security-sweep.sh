#!/usr/bin/env bash
# Pre-public security sweep (T-052, SETUP.md §7).
#
# Every check here must pass before the repository is made public. Missing
# tooling is reported as SKIP, not silently as a pass -- a sweep that quietly
# does nothing is worse than no sweep.
set -uo pipefail
cd "$(dirname "$0")/.."

fail=0
skip=0

ok()   { printf "  %-28s ok\n"   "$1"; }
bad()  { printf "  %-28s FAIL\n" "$1"; fail=1; }
miss() { printf "  %-28s SKIP (%s not installed)\n" "$1" "$2"; skip=1; }

echo "SHADOWBOOK security sweep"

# 1. No secret material in the tree. .env is gitignored; .env.example must
#    contain only placeholders.
if git ls-files --error-unmatch .env >/dev/null 2>&1; then
    bad ".env not committed"
else
    ok ".env not committed"
fi

if grep -REn "(PASSWORD|SECRET|TOKEN|API_KEY)[[:space:]]*[:=][[:space:]]*[^\$#[:space:]]" \
        --include="*.yml" --include="*.yaml" --include="*.json" --include="*.go" \
        --include="*.py" --include="*.sh" . 2>/dev/null \
        | grep -v "change-me" | grep -v "shadowbook:shadowbook" | grep -v '\${' | grep -q .; then
    bad "no hardcoded credentials"
    grep -REn "(PASSWORD|SECRET|TOKEN|API_KEY)[[:space:]]*[:=][[:space:]]*[^\$#[:space:]]" \
        --include="*.yml" --include="*.yaml" --include="*.go" . 2>/dev/null \
        | grep -v "change-me" | grep -v "shadowbook:shadowbook" | grep -v '\${' | head -5
else
    ok "no hardcoded credentials"
fi

# 2. Compose images pinned by digest (D-017).
if grep -q "image:.*@sha256:" docker-compose.yml 2>/dev/null; then
    ok "compose images digest-pinned"
else
    bad "compose images digest-pinned"
    echo "      run scripts/pin-digests.sh on a machine with registry access (D-017)"
fi

# 3. go.work must NOT be committed: it exists only for restricted build
#    environments and is wrong everywhere else (D-016).
if git ls-files --error-unmatch go.work >/dev/null 2>&1; then
    bad "go.work not committed"
else
    ok "go.work not committed"
fi

# 4. go.sum regenerated with the checksum database ON (D-016).
if [ -f go.sum ]; then
    ok "go.sum present"
else
    bad "go.sum present"
    echo "      run 'go mod tidy' with GOSUMDB enabled (D-016)"
fi

# 5. Dependency audits.
if command -v govulncheck >/dev/null 2>&1; then
    if govulncheck ./... >/dev/null 2>&1; then ok "govulncheck"; else bad "govulncheck"; fi
else
    miss "govulncheck" "govulncheck"
fi

if command -v uv >/dev/null 2>&1 && uv run --quiet pip-audit --version >/dev/null 2>&1; then
    if uv run pip-audit >/dev/null 2>&1; then ok "pip-audit"; else bad "pip-audit"; fi
else
    miss "pip-audit" "pip-audit"
fi

if command -v gitleaks >/dev/null 2>&1; then
    if gitleaks detect --no-banner --redact >/dev/null 2>&1; then ok "gitleaks"; else bad "gitleaks"; fi
else
    miss "gitleaks" "gitleaks"
fi

# 6. The honesty line the README must never lose.
if grep -q "not a production system" README.md; then
    ok "README production disclaimer"
else
    bad "README production disclaimer"
fi

echo
if [ "$fail" -ne 0 ]; then
    echo "security sweep: FAILED"
    exit 1
fi
if [ "$skip" -ne 0 ]; then
    echo "security sweep: passed, with skips. Install the missing tools before going public."
    exit 0
fi
echo "security sweep: PASS"
