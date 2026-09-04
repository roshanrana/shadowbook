#!/usr/bin/env bash
# Regenerate the local-only go.work described in decisions.md D-016.
#
# WHO NEEDS THIS: nobody with normal network access. If `go build ./...` works
# on your machine, you do not need this script and should not run it -- a
# workspace file that shadows go.mod is a liability, not a feature.
#
# WHO DOES: a build environment that reaches github.com but not
# proxy.golang.org, golang.org or google.golang.org -- which is where this
# project was implemented. There, vanity import paths cannot be resolved at
# all, so each one is mapped to its GitHub mirror at the SAME version that
# go.mod requires. go.mod stays clean and correct for everyone else.
#
# The mapping is the whole content of this script. It is checked in precisely
# because the go.work it produces is gitignored: without it, the recipe for
# building in a restricted environment lived only in a shell history.
set -euo pipefail

cd "$(dirname "$0")/.."

# Resumable on purpose. Each resolution pass downloads a module, so a run that
# is interrupted has done real work; starting over from an empty file would
# throw it away. Re-running picks up where it stopped.
RESUMING=0
[[ -f go.work ]] && RESUMING=1

# Read the language version from go.mod rather than hard-coding it. A workspace
# declaring an older Go than the module requires is rejected outright, and
# `go mod tidy` raises the module's floor whenever a dependency does -- which is
# exactly what happened when franz-go's kfake pulled it from 1.23 to 1.23.8.
GOVERSION=$(awk '/^go /{print $2; exit}' go.mod)
: "${GOVERSION:=1.23}"

# vanity path -> GitHub mirror. Versions are read from go.mod so this file can
# never drift from it: a bumped dependency needs no edit here.
declare -A MIRROR=(
    [golang.org/x/crypto]=github.com/golang/crypto
    [golang.org/x/exp]=github.com/golang/exp
    [golang.org/x/sync]=github.com/golang/sync
    [golang.org/x/text]=github.com/golang/text
    [golang.org/x/net]=github.com/golang/net
    [golang.org/x/sys]=github.com/golang/sys
    [golang.org/x/time]=github.com/golang/time
    [golang.org/x/mod]=github.com/golang/mod
    [golang.org/x/tools]=github.com/golang/tools
    [golang.org/x/term]=github.com/golang/term
    [golang.org/x/oauth2]=github.com/golang/oauth2
    [golang.org/x/telemetry]=github.com/golang/telemetry
    [google.golang.org/protobuf]=github.com/protocolbuffers/protobuf-go
    [gopkg.in/yaml.v3]=github.com/go-yaml/yaml/v3
    [gopkg.in/check.v1]=github.com/go-check/check
    [gonum.org/v1/gonum]=github.com/gonum/gonum
    [gonum.org/v1/netlib]=github.com/gonum/netlib
)

version_of() { # module path -> version in go.mod, empty if absent
    go list -m -f '{{.Version}}' "$1" 2>/dev/null || true
}

if (( RESUMING )); then
    echo "go.work exists -- resuming resolution rather than rebuilding it."
else
{
    echo "go $GOVERSION"
    echo
    echo "use ."
    echo
    echo "replace ("
} > go.work
fi

# Sorted, so the generated file is byte-identical between runs. Bash iterates
# an associative array in hash order; a workspace whose contents reshuffled on
# every regeneration would make "did my build inputs change?" unanswerable.
added=0
if (( ! RESUMING )); then
    for vanity in $(printf '%s\n' "${!MIRROR[@]}" | sort); do
        ver=$(awk -v m="$vanity" '$1 == m { print $2 }' go.mod | head -1)
        [[ -n "$ver" ]] || continue
        printf '\t%s => %s %s\n' "$vanity" "${MIRROR[$vanity]}" "$ver" >> go.work
        added=$((added + 1))
    done
    echo ")" >> go.work
fi

# Direct requirements are only half the problem: the module graph also pulls
# vanity paths that go.mod never names (vegeta needs golang.org/x/net, which is
# nobody's direct dependency), at versions go.mod cannot tell us.
#
# Rather than keep a second hand-maintained version table that would silently
# drift from the graph, ask the build. `go build` names the exact module and
# version it could not resolve; add that one replace and ask again. This
# terminates because each pass resolves one more node of a finite graph.
export GOPROXY="${GOPROXY:-direct}" GOSUMDB="${GOSUMDB:-off}" GOPRIVATE="${GOPRIVATE:-*}"

for _ in $(seq 1 25); do
    err=$(go build ./... 2>&1) && break
    # The error spans two lines:
    #     github.com/x/y@v1 requires
    #         golang.org/x/net@v0.27.0: unrecognized import path ...
    # Both lines carry a module@version, and only the SECOND is the one that
    # failed. Anchor on "unrecognized import path" so the requiring module is
    # never mistaken for the missing one -- that mistake reads as "already
    # replaced but still unresolved" and aborts a run that was fine.
    #
    # Host-agnostic on purpose. An earlier version matched a fixed list of
    # vanity hosts and broke the first time the graph pulled a new one
    # (gonum.org, via vegeta's tdigest). What identifies the failure is the
    # message, not the hostname.
    missing=$(printf '%s\n' "$err" \
        | sed -n 's/^[[:space:]]*\([^[:space:]]*\)@\([^:]*\): unrecognized import path.*/\1@\2/p' \
        | head -1 || true)
    [[ -n "$missing" ]] || { printf '%s\n' "$err" >&2; echo "dev-workspace: build failed for a reason this script cannot fix" >&2; exit 1; }

    path=${missing%@*}
    ver=${missing#*@}
    mirror=${MIRROR[$path]:-}
    if [[ -z "$mirror" ]]; then
        echo "dev-workspace: no GitHub mirror known for $path -- add one to the MIRROR table" >&2
        exit 1
    fi
    if grep -q "	$path =>" go.work; then
        echo "dev-workspace: $path is already replaced but still unresolved; giving up" >&2
        printf '%s\n' "$err" >&2
        exit 1
    fi
    # Insert before the closing paren, then re-sort the block for determinism.
    sed -i "s|^)\$|\t$path => $mirror $ver\n)|" go.work
    body=$(sed -n '/^replace ($/,/^)$/p' go.work | sed '1d;$d' | sort)
    printf 'go %s\n\nuse .\n\nreplace (\n%s\n)\n' "$GOVERSION" "$body" > go.work
    added=$((added + 1))
done

echo "go.work written with $added replace directives."
echo
echo "Now run, in this shell:"
echo "  export GOPROXY=direct GOSUMDB=off GOPRIVATE='*' GOTOOLCHAIN=local"
echo
echo "Delete go.work before running 'make check' the way CI does (D-016)."
