#!/usr/bin/env bash
# Rewrite every image tag in docker-compose.yml to an immutable digest.
# Run once on a machine with registry access, then commit the result.
# Required before the repo goes public (SETUP.md §7, decisions.md D-017).
set -euo pipefail
cd "$(dirname "$0")/.."
grep -oP '(?<=image: )\S+' docker-compose.yml | sort -u | while read -r img; do
  case "$img" in *@sha256:*) echo "already pinned: $img"; continue;; esac
  digest=$(docker buildx imagetools inspect "$img" --format '{{.Manifest.Digest}}' 2>/dev/null) \
    || { echo "could not resolve $img" >&2; exit 1; }
  echo "$img -> $digest"
  sed -i "s|image: ${img}$|image: ${img%%:*}@${digest}|" docker-compose.yml
done
echo "done. review the diff, then commit."
