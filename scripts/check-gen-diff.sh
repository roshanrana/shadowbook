#!/usr/bin/env bash
# Generated protobuf code is CHECKED IN (D-013). CI fails if regenerating
# produces a diff -- that, not discipline, is what keeps Go and Python in step,
# and it is what lets `make check` run with no network (NFR-8).
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/go" "$tmp/python"
(cd contracts && protoc --go_out="$tmp/go" --go_opt=paths=source_relative \
    --python_out="$tmp/python" --pyi_out="$tmp/python" \
    -I . $(find . -name '*.proto' | sort))
if ! diff -r -q "$tmp/go" gen/go >/dev/null 2>&1 || ! diff -r -q "$tmp/python" gen/python >/dev/null 2>&1; then
  echo "gen/ is stale: a .proto changed without running 'make proto'." >&2
  diff -r "$tmp/go" gen/go || true
  diff -r "$tmp/python" gen/python || true
  exit 1
fi
echo "gen/ matches contracts/"
