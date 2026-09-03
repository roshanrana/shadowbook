# T-007 — Protobuf contracts, buf config, generated code checked in
Status: todo      Milestone: M0   Wave: 3
Depends on: T-002

## Goal
`Money`, `Entry`, `PostingEvent` and `MovementEvent` exist as proto definitions, generate for Go and Python, and the generated code is committed. `buf breaking` and a `gen/` diff check guard the frozen contract.

## Context
- LLD §4.1 holds the frozen definitions. Copy them exactly; this is the contract two languages depend on.
- D-013: generated code is **checked in** and CI fails if regenerating produces a diff. That, not discipline, keeps the languages in step — and it is what keeps `make check` offline (NFR-8).
- `make proto` is currently a stub that exits 1. This task wires it.
- `Money` mirrors `internal/money.Amount` exactly (T-005) — same three fields, same meanings.

## Contracts to honor
LLD §4.1 verbatim. `MovementEvent.message_id` is the inbox primary key (LLD §3.3) — it is an identity, not a hint.

## File scope
Create: `contracts/buf.yaml`, `contracts/buf.gen.yaml`, `contracts/shadowbook/v1/posting.proto`, `contracts/shadowbook/v1/movement.proto`, `gen/go/shadowbook/v1/*.pb.go`, `gen/python/shadowbook/v1/*_pb2.py*`, `scripts/check-gen-diff.sh`
Modify: `Makefile` (the `proto` target only), `.github/workflows/check.yml` (add the `buf breaking` and gen-diff steps)

## Suggested steps
1. Verify the current buf module layout and plugin names via GitHits before writing `buf.gen.yaml` — CLAUDE.md says the brief may be stale on specifics.
2. Write the two `.proto` files from LLD §4.1 without embellishment.
3. `buf generate` into `gen/go` and `gen/python`; commit the output.
4. `scripts/check-gen-diff.sh`: regenerate into a temp dir, diff against `gen/`, exit non-zero on difference.
5. Wire `make proto` to `buf generate`, and add the diff check to `make check`.

## Acceptance criteria
- [ ] `make proto` regenerates and leaves `git status` clean
- [ ] `scripts/check-gen-diff.sh` exits non-zero if a `.proto` is edited without regenerating (test it, then revert)
- [ ] `buf lint` passes
- [ ] `buf breaking` against `main` is wired into CI
- [ ] Generated Go compiles; generated Python imports under `mypy --strict` (add a stub exclusion if unavoidable, and say so in handoff)
- [ ] `make check` still passes offline — no step fetches a plugin at check time

## Out of scope
The `extract.proto` documentation-only file (LLD §4.3) — the extract wire format is the text file and is defined in T-031. Any producer or consumer code.

## Handoff notes
_(filled by the worker)_
