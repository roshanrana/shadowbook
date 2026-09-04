# SHADOWBOOK

**A digital-twin harness for core-ledger migration.**

Run a new double-entry ledger as a read-only shadow of a legacy core, reconcile
the two continuously, and measure two things nobody usually measures: **how long
each undocumented legacy behaviour takes to surface**, and **what each consumer
design actually costs when the message broker dies mid-payday**.

Both results are reproducible from a seed, and both are generated from run
artefacts rather than written by hand.

> Portfolio work built to production constraints. No real money, no real data,
> not a production system. `docs/ship-report.md` is the honest account of what
> is and is not measured.

---

## Why this problem

Core migrations do not fail on the happy path. They fail in the last two percent
of reconciliation breaks, and those are almost always *undocumented behaviour in
the incumbent* — a rounding rule, a cut-off boundary, a fee waiver nobody wrote
down, a holiday the old system does not observe.

You cannot test against behaviour nobody has written down. You can only run both
systems side by side and watch where they disagree. That is what this is: a
shadow ledger, a legacy simulator carrying twelve deliberate divergences, a
reconciler that finds them at three grains — and an honest measurement of how
long each one took to show up.

---

## The two findings

### Finding 1 — time-to-discovery, and the gap between detecting and explaining

All **12 of 12** seeded quirks are detected. Each is measured *in isolation*
against a control run with every quirk disabled, which is the only way "business
days until this surfaced" means what it says.

The more useful result sits underneath that headline:

| | |
|---|---|
| Detected at all | **12 of 12** |
| Isolated to a single quirk | **3 of 12** |
| Business days to first detection | median 2.5, range 1–26 |

**Reconciliation is a tripwire, not a diagnosis.** Every seeded behaviour
surfaced, most within the first working week — but only three produced a break
signature naming exactly one cause. The reason is structural rather than a
tuning gap: signatures are integer predicates over a delta, and distinct
behaviours produce the same delta shape. More rules cannot fix it, because the
information needed is not in the delta.

Time-to-discovery is floored by **cadence**, not by sensitivity — a month-end
quirk cannot be found before month end. And window *placement* matters as much
as length: W1 opens on 2028-02-28, so a leap-day month end falls on business day
2 and the next not until day 25.

### Finding 2 — delivery semantics under real broker loss

Measured against three Redpanda brokers at replication factor 3, killed and
restarted on a quorum-preserving schedule, 36,000 movements per run, three runs
per configuration.

| Config | Applied | Lost | Duplicated | Invariant |
|---|---|---|---|---|
| A — at-most-once | 36000 [35975–36000] | **0 [0–25]** | 0 [0–8472] | held |
| B — at-least-once | 36000 | 0 | 4959 [0–8950] | held |
| C — at-least-once + inbox | 36000 | 0 | **0** | held |

**A did both, and that is the row worth reading twice.** It is the only
configuration that lost anything — 25 movements produced, acknowledged by the
cluster, never applied. It also duplicated, because at-most-once holds only
while the offset commit survives and a coordinator failover can lose the commit.
It buys "no duplicates" while commits are durable, and pays with real loss when
they are not.

C duplicated nothing in any run, suppressed by `inbox_pkey` — a database
constraint rather than a race that usually goes the right way.

**The zero-sum invariant held in all nine runs.** A duplicated posting is
perfectly balanced and completely wrong: correctness at the row level does not
imply correctness at the ledger level. That gap is precisely why a second source
of truth exists.

---

## What building it actually found

The most interesting document here is `docs/field-notes.md`, because it is the
least flattering.

**Five defects sat in a chaos profile that had been written, reviewed and
committed six weeks earlier, and never once started.** Container names the kill
schedule could not match. Brokers advertising an address unreachable from the
host. Four cluster properties set as *node* properties and silently ignored —
one of them a Kafka property name that **does not exist in Redpanda at all**,
while the config header advertised the guarantee on its strength. And
credentials that broke the documented setup path on every fresh clone.

Only one of the five failed loudly. Had it been ignored like its three
neighbours, the cluster would have started, the sweep would have run, and the
numbers would have looked entirely reasonable while resting on four settings
that did nothing.

**Four more were in the measurement harness**, each producing results that were
internally consistent and wrong: a drain detector reporting 122,886 "lost"
records that were sitting on the broker intact; sweep-to-sweep contamination
through reused topic names, which reported 44,746 movements applied against
36,000 sent; a duplication count that was structurally zero for the two
configurations without an inbox; and a ledger that exited whenever a broker went
away, because retriable errors were classified on the fetch path and not the
commit path.

The pattern across all of them: **the failures worth guarding against are the
ones that produce a plausible number.** An exception is easy to notice. A
confidently wrong table is what reaches a decision-maker. There are now seven
guards that refuse to render a finding rather than render a doubtful one.

---

## The system

| Component | Language | Responsibility |
|---|---|---|
| `ledger` | Go | Double-entry, append-only, derived balances with checkpoints, ledger/available/pending with expiring holds, EOD accrual, idempotency as a database constraint, transactional outbox, a consumer with four swappable delivery modes, Prometheus metrics including a live global-invariant check |
| `legacy-sim` | Python | Deterministic transaction stream and header/detail/trailer EOD extracts, with twelve switchable quirks defined as **data** in `legacy-sim/quirks.yaml` |
| `reconcile` | Python | Three grains — transaction, account-day, control total — plus timing / model-difference / defect classification, ageing, and per-quirk time-to-discovery |
| `harness` | Go | Open-model load (payday, month-end, hot keys), scripted broker kills, the A–D ablation runner, and deterministic report rendering |

One gate: **`make check`** — format, lint, `mypy --strict`, `go vet`, unit,
integration, `-race`. CI runs the same command and adds nothing to it.

~10,900 lines of Go, ~4,500 of Python, 243 tests, six numbered migrations,
33 recorded decisions.

---

## Quickstart

```bash
cp .env.example .env
make doctor      # says exactly what this machine can and cannot run
make demo        # Finding 1, both windows, ~2 seconds, no containers needed
```

`make demo` needs only [`uv`](https://docs.astral.sh/uv/). It regenerates
`reports/FINDINGS.md` from a seed; the same seed produces byte-identical output.

<details>
<summary>The full stack</summary>

```bash
make up          # PostgreSQL
make check       # the gate

make up-chaos    # three Redpanda brokers, RF=3
make ablate      # Finding 2 against the real cluster
make report

make ablate-sim  # the same sweep with no Docker at all -- results are
                 # labelled `simulated` and refused as Finding 2
make down
```

`docs/runbook.md` covers what to do when any of it misbehaves.
</details>

---

## Documentation

| Read this | For |
|---|---|
| **`docs/guided-tour.md`** | the short path through the repo — ten minutes, thirty, or ninety |
| **`reports/FINDINGS.md`** | the results, generated and never hand-edited |
| **`docs/field-notes.md`** | every defect running it found, and what each one generalises to |
| **`docs/domain-model.md`** | money, dates, day counts, balances, identity — and why each rule is the way it is |
| **`docs/ship-report.md`** | the go/no-go: what is measured, what is not, what is written but never run |
| `docs/design/` | requirements → HLD → LLD → execution plan |
| `docs/design/decisions.md` | 33 numbered decisions, append-only, including the ones that were wrong |
| `docs/runbook.md` | operating it, and what to do when it misbehaves |

---

## Named adversarial scenarios

Each maps to a test by name — the scenario list is not aspirational.

| Scenario | Test |
|---|---|
| all twelve quirks detected, by id | `test_every_quirk_is_detected_in_at_least_one_window` |
| Q5 only in W2, Q6 only in W1 | `test_q5_is_detected_only_in_w2_and_q6_only_in_w1` |
| 64 concurrent requests, same key → one effect | `TestIdempotencyRace64` |
| append-only and zero-sum enforced by the database | `TestAppendOnly`, `TestZeroSumEnforcedAtCommit` |
| unbalanced posting rejected even if validation is bypassed | `TestDatabaseRejectsUnbalancedEvenIfValidationBypassed` |
| mode A loses a batch after committing | `TestModeALosesRecordsOnFailureAfterCommit` |
| B duplicates under redelivery, C and D do not | `TestRedeliveryDuplicatesInBButNotInC/{B,C,D}` |
| every mode holds the global invariant | `TestAllModesPreserveTheGlobalInvariant` |
| a commit is durable the moment `Commit` returns | `TestKafkaCommitIsDurableOnReturn` |
| late / redelivered / truncated extracts | `test_a_late_extract_is_flagged_but_still_used`, `test_a_byte_identical_redelivery_is_not_double_counted`, `test_a_truncated_extract_is_recorded_not_raised` |
| a trailer that lies about its total | `test_a_trailer_that_lies_about_the_total_is_caught` |
| same seed → byte-identical extracts | `test_two_runs_of_the_same_seed_are_byte_identical` |
| Go and Python calendars agree over 1,096 days | `test_business_days_match_go` |
| a simulated run can never render as a finding | `TestTableRefusesFakeBrokerArtefacts` |
| a run cut off mid-drain can never render as a finding | `TestTableRefusesRunsThatNeverDrained` |

---

## How it was built

Under a gated, document-first lifecycle: requirements, high-level design,
low-level design and a task-level execution plan, each approved before the next
began. **No application code exists before the commit that approved the plan**,
and the git history shows it.

The schema in the LLD was executed against PostgreSQL 16 *before* the design was
approved, which caught two defects — a mistyped `BIGSERIAL`, and a balance query
that returned no row at all for an account with no checkpoint yet, so a new
account read as "balance unknown" rather than zero.

Decisions are numbered and append-only, **including the ones that turned out to
be wrong**. D-019 records an approved dependency version that could not build on
the project's own Go floor. D-030 records a measurement that had to be
discarded. D-031 records the third time the same isolation lesson was learned:
whatever outlives the process is what contaminates the next run.

---

## What this does not prove

- **The quirks are seeded and known.** This calibrates a twin's reconciliation;
  it does not discover unknown behaviour.
- **A simulator is not a core.** A real incumbent has decades of divergences,
  and they interact in ways this cannot reproduce.
- **Single-box numbers are not production numbers.** NFR-1's throughput target
  was not met on the available hardware.
- **Finding 2 ran at 200 movements/s, not the specified 1,000/s**, because the
  consumer sustains ~280/s on that host. Valid as a comparison between
  configurations under identical chaos; not the specified rate.
- **Configuration D is implemented and has never run.** `kfake` cannot verify
  transactional producer ids, so only a real cluster can.

---

## Related work

HARBORMASTER (file-arrival control for reconciliation) · LEDGERLENS
(bounded-LLM reconciliation adjudication) · PROVENANCE (verifiable,
tenant-isolated LLM inference) · REGLENS · MARKETSAGE —
[github.com/roshanrana](https://github.com/roshanrana)
