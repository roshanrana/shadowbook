# SHADOWBOOK — Overview

**What it is:** a digital-twin harness for core-ledger migration. A new double-entry ledger runs as a read-only shadow of a simulated legacy core; a reconciler compares them continuously at three grains; and two things that are rarely measured get measured: how long each undocumented legacy behaviour takes to surface, and what each message-consumer design actually costs when the broker dies mid-payday.

**Read this if** you want the problem and the two findings in context. [SHOWCASE.md](SHOWCASE.md) is the tour of the features; [`guided-tour.md`](guided-tour.md) is the deeper walk through the seams that repay attention.

---

## The setting

Every institution with a core ledger older than its engineers eventually replaces it. The programme plan is always the same: build the new ledger, run it in parallel, reconcile, cut over when the breaks reach zero. The programme always runs late for the same reason: the last two percent of breaks are not bugs in the new system. They are undocumented behaviour in the old one. A rounding rule nobody wrote down. A fee waiver that lives in a stored procedure from 2004. A cut-off boundary that shifts on a holiday the old system does not observe.

You cannot write a test for behaviour nobody has documented. You can only run both systems side by side and watch where they disagree, and then you can ask a question programme managers rarely get a number for: *how long does it take for each kind of divergence to show up?*

SHADOWBOOK is built to answer that, with a legacy simulator carrying twelve deliberate, switchable quirks so the answer can be measured rather than estimated.

## Finding 1: detection is not diagnosis

All twelve seeded quirks are detected, most within the first working week. Measured in isolation, each against a control run with every quirk disabled, which is the only way "business days until this surfaced" means what it says.

| | |
|---|---|
| Detected at all | 12 of 12 |
| Isolated to a single quirk | 3 of 12 |
| Business days to first detection | median 2.5, range 1 to 26 |

The useful result is the middle row. Reconciliation is a tripwire, not a diagnosis. Break signatures are integer predicates over a delta, and distinct behaviours produce the same delta shape, so only three of twelve quirks produced a signature naming exactly one cause. More rules cannot fix this; the information is not in the delta. A migration programme that budgets for detection but not for attribution will find its breaks and then spend the real time working out what they mean.

Time-to-discovery is floored by cadence, not sensitivity: a month-end quirk cannot be found before month end. Window placement matters as much as window length.

## Finding 2: delivery semantics under real broker loss

Three Redpanda brokers at replication factor 3, killed and restarted on a quorum-preserving schedule, 36,000 movements per run, three runs per configuration.

| Config | Applied | Lost | Duplicated | Zero-sum invariant |
|---|---|---|---|---|
| A: at-most-once | 36000 [35975–36000] | **0 [0–25]** | 0 [0–8472] | held |
| B: at-least-once | 36000 | 0 | 4959 [0–8950] | held |
| C: at-least-once + inbox | 36000 | 0 | **0** | held |

Configuration A both lost and duplicated. It is the only configuration that lost anything (25 movements produced, acknowledged by the cluster, never applied), and it duplicated because at-most-once holds only while the offset commit survives a coordinator failover. It buys "no duplicates" while commits are durable and pays with real loss when they are not. Configuration C duplicated nothing in any run, suppressed by a database constraint rather than a race that usually goes the right way.

The zero-sum invariant held in all nine runs. A duplicated posting is perfectly balanced and completely wrong: row-level correctness does not imply ledger-level correctness, which is exactly why a second source of truth exists.

## What building it found

The most valuable document in the repository is [`field-notes.md`](field-notes.md), because it is the least flattering. Five defects sat in a chaos profile that had been written, reviewed and committed six weeks earlier and never once started: container names the kill schedule could not match, brokers advertising an unreachable address, four cluster properties set as node properties and silently ignored (one a Kafka property that does not exist in Redpanda at all), and credentials that broke the documented setup on every fresh clone. Only one of the five failed loudly.

Four more were in the measurement harness, each producing numbers that were internally consistent and wrong. The pattern: **the failures worth guarding against are the ones that produce a plausible number.** There are now seven guards that refuse to render a finding rather than render a doubtful one.

## The system

| Component | Language | Responsibility |
|---|---|---|
| `ledger` | Go | Double-entry, append-only, derived balances with checkpoints, ledger/available/pending with expiring holds, end-of-day accrual, idempotency as a database constraint, transactional outbox, a consumer with four swappable delivery modes, Prometheus metrics including a live global-invariant check |
| `legacy-sim` | Python | Deterministic transaction stream and header/detail/trailer EOD extracts; twelve switchable quirks defined as data in `legacy-sim/quirks.yaml` |
| `reconcile` | Python | Three grains (transaction, account-day, control total), timing / model-difference / defect classification, ageing, per-quirk time-to-discovery |
| `harness` | Go | Open-model load (payday, month-end, hot keys), scripted broker kills, the A–D ablation runner, deterministic report rendering |

About 10,900 lines of Go, 4,500 of Python, 243 tests, six numbered migrations, 33 recorded decisions. One gate, `make check`, and CI runs the same command.

## How it was built

Under a gated, document-first lifecycle: requirements, high-level design, low-level design and a task-level execution plan, each approved before the next began. No application code exists before the commit that approved the plan. The LLD's schema was executed against PostgreSQL 16 before the design was approved, which caught a mistyped `BIGSERIAL` and a balance query that returned no row for an account with no checkpoint, so a new account read as "unknown" rather than zero.

Decisions are numbered and append-only, including the wrong ones. D-019 records an approved dependency version that could not build on the project's Go floor. D-030 records a measurement that had to be discarded. D-031 records the third time the same isolation lesson was learned.

## What it does not prove

The quirks are seeded and known; this calibrates a twin's reconciliation and does not discover unknown behaviour. A simulator is not a core. Single-box numbers are not production numbers, and Finding 2 ran at 200 movements per second rather than the specified 1,000 because the consumer sustains about 280 on that host. Configuration D is implemented and has never run, because the in-process Kafka fake cannot verify transactional producer ids.

## Where it sits among the other projects

SHADOWBOOK is the ledger-to-ledger reconciliation. [HARBORMASTER](https://github.com/roshanrana/Harbormaster) controls what reaches a reconciliation engine from outside; [LEDGERLENS](https://github.com/roshanrana/LedgerLens) is the engine with bounded AI adjudication. All three treat named adversarial fixtures and refusal-to-guess as first-class design.
