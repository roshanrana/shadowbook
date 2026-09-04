# Guided tour — the short path through this repository

Written on the assumption that your time is the scarce resource. Three routes,
depending on how much of it you have.

---

## Ten minutes — read four things

1. **`reports/FINDINGS.md`** — the output. Two findings, generated from run
   artefacts, never hand-edited. Start with Finding 1a (detection is not
   attribution) and the Finding 2 table.
2. **`docs/ship-report.md`** — the go/no-go. Written to be believed rather than
   to impress: §3 lists requirements *not* met, §4 lists code written but never
   run.
3. **`docs/field-notes.md`** — what running it found. The most interesting
   document here, and the least flattering.
4. **`docs/design/decisions.md`** — 33 numbered decisions, append-only. D-019,
   D-025, D-030 and D-031 are the ones where the design was wrong and the
   record says so.

---

## Thirty minutes — run it

```bash
cp .env.example .env
make doctor        # says exactly what this machine can and cannot run
make demo          # Finding 1, both windows, ~2 seconds, no containers
```

`make demo` needs only `uv`. It regenerates `reports/FINDINGS.md` from a seed,
and the same seed gives byte-identical output.

Then read the generated report against the code that produced it:

| In the report | Produced by |
|---|---|
| the per-quirk table | `reconcile/src/reconcile/discovery.py` |
| the three grains | `reconcile/src/reconcile/grains.py` |
| the classification rules | `reconcile/src/reconcile/classify.py` |
| the twelve quirks themselves | `legacy-sim/quirks.yaml` — data, not code |

For the whole gate, including the database:

```bash
make up            # PostgreSQL
make check         # format, lint, mypy --strict, go vet, unit, integration, -race
```

---

## Ninety minutes — the interesting seams

Roughly in order of how much they repay attention.

### 1. Idempotency as a database constraint

`internal/ledger/posting/posting.go`

The claim is inserted *first*; a `23505` unique violation on the primary key
**is** the replay detection. There is no read-then-write, so there is no race.
`TestIdempotencyRace64` releases 64 goroutines simultaneously on the same key
and asserts one posting, two entries, one outbox row, and 64 identical
responses.

### 2. The invariants that live in SQL

`migrations/0003_invariants.sql`

Append-only enforced by a trigger; the zero-sum rule enforced by a **deferred**
constraint trigger that fires at `COMMIT`, because entries arrive one row at a
time and the question is only meaningful once the posting is whole.

`TestDatabaseRejectsUnbalancedEvenIfValidationBypassed` bypasses the Go
validation entirely and asserts the database still refuses.

### 3. Four delivery modes that differ in one thing

`internal/ledger/consumer/consumer.go`

Mode C is written first and the others are expressed as documented deviations
from it. The only structural difference between them is **when `Commit` is
called relative to applying the effect** — which is why autocommit is disabled,
and why a background committer on a timer would flatten all four into the same
measurement.

Then read `reports/FINDINGS.md` Finding 2 for what that difference costs against
a real cluster being killed.

### 4. The guards that refuse to publish a bad number

`internal/harness/ablation/artefact.go`

Seven refusals, each one added after something plausible-but-wrong nearly got
through. `Table` will not render a set of runs that came from an in-process
broker, or that mixes simulated with real, or that contains a run cut off
mid-drain, or that spans two sweeps. `docs/field-notes.md` explains what each
one caught.

### 5. Money and dates

`internal/money/`, `internal/bizdate/`

Small, total, and heavily tested — `money` is at 100% statement coverage.
`docs/domain-model.md` explains why each rule is the way it is; this is the
part where a wrong-but-plausible choice ships and is found years later.

---

## How the repository is laid out

```
cmd/            ledger, harness, goldencal — the three binaries
internal/
  money/        integer minor units, registry scale
  bizdate/      business dates, cut-off, day counts, rounding, calendar
  broker/       Producer/Consumer seam; in-process fake + franz-go
  ledger/       posting, balance, accrual, consumer, outbox, httpapi, obs, store
  harness/      load profiles, chaos schedule, ablation runner
migrations/     six numbered SQL files, embedded, forward-only
legacy-sim/     Python — deterministic legacy core with twelve quirks
reconcile/      Python — three grains, classification, discovery
report/         Jinja template + renderer for FINDINGS.md
docs/design/    requirements → HLD → LLD → execution plan, plus decisions
```

Roughly 10,900 lines of Go and 4,500 of Python, 243 tests.

---

## Reading the git history

The history is part of the artefact. It was built under a gated, document-first
process: **no application code exists before the commit that approved the plan.**
Commit messages carry the reasoning, not the diff — most of the defects in
`docs/field-notes.md` are explained in the commit that fixed them.

Three worth reading in full:

- `Sweep isolation: run ids repeat, so topics and groups did too`
- `The first real-cluster sweep measured two of my own defects`
- `compose: stop setting cluster properties as node start flags`

---

## What to be sceptical about

Stated plainly here so you do not have to go looking:

- **The quirks are seeded and known.** This calibrates a twin's reconciliation;
  it does not discover unknown behaviour.
- **A simulator is not a core.** `legacy-sim` is a few thousand lines with
  twelve deliberate divergences. A real incumbent has decades of them.
- **Single-box numbers are not production numbers.** NFR-1's throughput target
  was not met on the available hardware, and the ship report says so.
- **Configuration D is implemented and has never run.** `kfake` cannot verify
  transactional producer ids, so only a real cluster can.
- **Finding 2 used a 200/s rate, not the specified 1,000/s**, because the
  consumer sustains ~280/s on that hardware. The ablation compares
  configurations against each other under identical chaos, so a lower shared
  rate is still a valid experiment — but it is not the specified one.
