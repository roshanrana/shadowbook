# SHADOWBOOK — Showcase

The feature tour. [`guided-tour.md`](guided-tour.md) already walks the seams that repay the most attention (idempotency as a constraint, invariants in SQL, the outbox, the four consumer modes, the refusal guards); this document is the map of everything else, with the commands that show it.

## Two seconds, no containers

```bash
cp .env.example .env
make doctor      # what this machine can and cannot run
make demo        # Finding 1, both windows, regenerates reports/FINDINGS.md from a seed
```

Run `make demo` twice. The output is byte-identical, and `test_two_runs_of_the_same_seed_are_byte_identical` asserts that it stays so.

## The full stack

```bash
make up          # PostgreSQL
make check       # format, lint, mypy --strict, go vet, unit, integration, -race
make up-chaos    # three Redpanda brokers, RF=3
make ablate      # Finding 2 against the real cluster
make report
make down
```

`make ablate-sim` runs the same sweep with no Docker. Its results are labelled `simulated` and the report renderer refuses to present them as Finding 2. That refusal is a test: `TestTableRefusesFakeBrokerArtefacts`.

## Feature tour

### The ledger (`internal/ledger/`)

| Look at | What it shows |
|---|---|
| `posting/posting.go` | Idempotency as a database constraint: the claim is inserted first and a unique violation *is* the replay detection. No read-then-write, so no race. `TestIdempotencyRace64` releases 64 goroutines on one key and asserts one posting |
| `migrations/0003_invariants.sql` | Append-only enforced by trigger; zero-sum enforced by a deferred constraint trigger at `COMMIT`. `TestDatabaseRejectsUnbalancedEvenIfValidationBypassed` proves the database holds the line when the application does not |
| `balance/balance.go` | Ledger, available and pending balances derived from entries with checkpoints; expiring holds |
| `accrual/` | End-of-day accrual with day-count conventions from `internal/bizdate/` |
| `outbox/relay.go` | Transactional outbox; a posting and its event commit together or not at all |
| `consumer/consumer.go` | Four swappable delivery modes (A at-most-once, B at-least-once, C at-least-once with inbox, D transactional), the subject of Finding 2 |
| `obs/metrics.go` | Prometheus metrics including a live global zero-sum check |
| `httpapi/` | The read-only API the reconciler consumes |

### Money and dates (`internal/money/`, `internal/bizdate/`)

Minor-unit integer money with a currency registry; business-day calendars, holidays and day counts. `test_business_days_match_go` asserts that the Go and Python calendars agree over 1,096 days, because a calendar disagreement between the twin and the reconciler would look exactly like a legacy quirk.

### The legacy simulator (`legacy-sim/`)

| Look at | What it shows |
|---|---|
| `quirks.yaml` | Twelve quirks as data: rounding, cut-off, fee waivers, holiday observance, trailer totals. Each is switchable, so each can be measured in isolation |
| `src/legacy_sim/generator.py`, `extracts.py` | A deterministic transaction stream and header/detail/trailer EOD extracts from a seed |
| `src/legacy_sim/windows.py` | The two measurement windows, placed so that a leap-day month end falls on business day 2 |

### The reconciler (`reconcile/`)

| Look at | What it shows |
|---|---|
| `grains.py` | Transaction, account-day and control-total reconciliation |
| `classify.py` | Timing difference, model difference, or defect |
| `discovery.py` | Per-quirk time-to-discovery against the control run |
| `age.py` | Break ageing |
| `finding1.py` | Assembles Finding 1 from artefacts |

Scenarios worth reading by name: `test_a_late_extract_is_flagged_but_still_used`, `test_a_byte_identical_redelivery_is_not_double_counted`, `test_a_truncated_extract_is_recorded_not_raised`, `test_a_trailer_that_lies_about_the_total_is_caught`.

### The harness (`internal/harness/`)

| Look at | What it shows |
|---|---|
| `load/profiles.go` | Open-model load: payday, month-end, hot keys |
| `chaos/` | Scripted broker kills on a quorum-preserving schedule, driven through Docker |
| `ablation/run.go`, `sim.go`, `net.go` | The A–D ablation runner, its in-process simulation, and the drain detection that once reported 122,886 "lost" records sitting intact on the broker |
| `report/src/report/render.py` | Deterministic rendering of `reports/FINDINGS.md`; the seven guards that refuse doubtful findings live here and in `ablation/artefact.go` |

## Things worth noticing

- **Findings are generated, never written.** `reports/FINDINGS.md` is an artefact of a run. If the run is simulated, cut off mid-drain, or otherwise doubtful, the renderer refuses, and the refusals are tested.
- **The field notes are the point.** Nine defects, five in a chaos profile that had never been started and four in the measurement harness, each producing a plausible number. The document generalises each one.
- **Configuration A is the row to read twice.** It lost *and* duplicated, and the explanation (commit durability across coordinator failover) is the kind of thing that only shows up against a real cluster.
- **33 decisions, append-only, including the wrong ones.**

## Questions this project answers, and where

| Question | Where the answer lives |
|---|---|
| How long does an undocumented legacy behaviour take to surface in parallel run? | `reports/FINDINGS.md`, Finding 1, and `reconcile/src/reconcile/discovery.py` |
| Why did the breaks get found but not explained? | Finding 1a: signatures are predicates over a delta, and the information is not in the delta |
| Which consumer design should a ledger use? | Finding 2; configuration C, and the reason A is dangerous |
| How do you make a posting idempotent under concurrency? | `posting.go` and `TestIdempotencyRace64` |
| What keeps the ledger balanced if the application has a bug? | `migrations/0003_invariants.sql` |
| How do you know your measurement is not lying to you? | `docs/field-notes.md` and the seven refusal guards |
| What did you get wrong? | `docs/design/decisions.md`, D-019, D-025, D-030, D-031 |
