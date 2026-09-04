# Field notes — what only running it could find

Every defect below was found by executing the system, not by reading it. They
are collected here because the pattern across them is more useful than any one
of them, and because a project that lists only its successes is telling you
half of what it knows.

They fall into three groups, and the groups are the interesting part.

---

## Group 1 — Defects that made a measurement silently meaningless

These are the dangerous ones. Nothing failed. Every check was green. The output
was plausible, internally consistent, and wrong.

### A single RNG made the controlled experiment uncontrolled

Finding 1 measures each quirk *in isolation* against a control run. The
simulator drew transaction values from one sequential RNG, so enabling the quirk
that adds a business day (Q5) shifted every subsequent draw. The per-quirk runs
were not controlled experiments; they differed from the control in the quirk
*and* in every value after it.

Nothing about the output looked wrong. Transaction values are now
`hash(seed, account, date, index)`, so adding or removing a day changes only
that day.

### Six defects that each made one quirk permanently invisible

Each of these made a specific seeded behaviour undetectable, so "11 of 12
detected" would have been reported as a limit of reconciliation rather than a
bug in the harness:

| Defect | Effect |
|---|---|
| Transaction grain filtered per-day on **both** sides | A posting the legacy core moved to a different date could never match its counterpart — 361 timing differences read as missing records |
| Simulator **dropped** cut-off-rolled transactions instead of carrying them | The transactions Q2 exists to move simply vanished |
| Reconciler iterated the **documented** calendar | It never compared the one day Q5 exists on |
| Holds snapshotted at 23:59 | Both expiry rules agree by then; Q8 was invisible by construction |
| `isMonthEnd` compared `d.AddDays(1)` against `Date(y, m, d+1)` | Those normalise to the same date, so it was **always false** and month-end fees were never assessed |
| Q12 required EOD to run on a day the calendar excluded | 2028-04-01 is a Saturday, so the trigger could never fire |

### Basis rules demanded exact cross-multiplication

The day-count classifier compared two rounded integers for exact equality. Both
sides round once, so exact equality essentially never holds and the rule never
matched. Replaced with a one-denominator-per-side tolerance.

### Duplication was identically zero for half the configurations

Finding 2 computed `duplicated = postings − applied`. Modes A and B keep no
inbox and mint a fresh posting id per delivery, so `applied` *equals* `postings`
and the expression is structurally zero. Mode B was observed applying **26,221
effects for 12,000 movements** and reporting **zero duplicates**.

The fix is not just arithmetic — it is that A and B cannot distinguish a loss
from a compensating duplicate at all. Their counts are now labelled *net*, and
the report says why. The same architectural fact reappears in the latency
column: without an inbox, the system is not measurable from its own data.

---

## Group 2 — Defects that only a real cluster could reach

The chaos profile had been written six weeks earlier, reviewed, and committed.
It had never once been started. Five defects were sitting in it.

| Defect | Why no test could catch it |
|---|---|
| Chaos kills brokers **by name**; Compose generates `<project>-redpanda-1-1` | `docker kill redpanda-1` matched nothing. A failed kill is *recorded, not fatal* by design — so the sweep would have completed cleanly having killed nothing, reporting three identical rows |
| Brokers advertised `redpanda-1:9092`, resolvable only inside the container network | The harness runs on the host: it bootstraps fine, then receives metadata pointing at unreachable names and every produce fails *after* an apparently successful connection |
| Four cluster properties set as **node** properties | `--set redpanda.X` writes node config. All four were silently ignored, so the profile claimed guarantees (RF=3, minimum replication, transactions) it did not have |
| `unclean_leader_election_enabled` **does not exist in Redpanda** | A Kafka property name. Inert since the day it was written, while the compose header advertised "election off" on the strength of it |
| `.env.example` ships `change-me`; the Makefile hardcoded `shadowbook` | The documented path — `cp .env.example .env && make up && make check` — failed auth on every fresh clone. It survived because every environment it had ever run in created Postgres by some *other* means |

Only one of the five failed loudly. Had `enable_idempotence` been silently
ignored like its three neighbours, the cluster would have started, the sweep
would have run, and the numbers would have looked entirely reasonable while
resting on four settings that did nothing.

**The generalisable claim:** a configuration file is not evidence. Nothing in
that profile was tested, because "testing" it would have meant starting it, and
nothing in the test suite could. The single most valuable thing done in this
project was starting it for real.

---

## Group 3 — Defects in the measurement harness itself

Found by refusing to accept results that did not make sense.

### The ledger died when a broker went away

Fetch errors were classified so the ledger would survive a broker kill. The
**commit** path was not, so the identical defect sat live on a second code path.
Six of nine runs died at ~2m30s, which is the second scheduled kill.

franz-go reports a commit to a dying broker as *"broker closed the connection
immediately after a request was issued, which happens when SASL is required but
not provided: is SASL missing?"* — its generic handshake-drop text, which reads
convincingly like a misconfiguration and sent the investigation to the wrong
place. One run gave it away in plain terms: `dial tcp [::1]:29092: actively
refused`. Port 29092 is `redpanda-2`, killed on schedule.

> **Lesson worth keeping:** when an error class is reclassified, every call site
> that can raise it must be revisited at the same time. Fixing fetch and not
> commit left the same bug live for a week, and only a real cluster reached it.

### The drain detector invented 122,886 lost records

It capped *total* wait at three minutes and reported an error if the consumer
was still applying when the cap expired. At 240,000 movements per run it always
was — and everything it had not yet reached was recorded as permanent loss. One
run reported 122,886 "lost" records that were sitting on the broker, intact.

Every fixed parameter agreed across those runs, so every existing guard would
have rendered that table without complaint. The cap now bounds **stalls**, not
progress, and artefacts record `Drained`; a table containing an undrained run is
refused.

### Runs were isolated; sweeps were not

Each run got its own database, consumer group and topic. Run ids repeat *between*
sweeps — `payday-C-1` every time — so the second sweep attached to the first
sweep's topics and groups and consumed 240,000 records from an experiment already
discarded.

Mode C exposed it by reporting **44,746 distinct movements applied against
36,000 sent**, which the inbox schema makes impossible. A and B concealed the
identical contamination behind a `min(effects, sent)` and reported the surplus as
duplication, where it looked like a finding.

> **Third time this project learned the same lesson:** whatever outlives the
> process is what contaminates the next run. The database was isolated before
> the log; the log was isolated per run before it was isolated per sweep. Each
> time, the surviving state produced results that were internally consistent and
> wrong.

---

## What the guards look like now

Each of the above is now a refusal rather than a comment, because a comment does
not stop anyone:

| Guard | Refuses |
|---|---|
| `BrokerFake` prefix | a table built from an in-process broker that cannot fail |
| `BrokerSim` prefix + `Kind()` | a simulated run rendering as a real one, or a table mixing kinds |
| `Drained` | a table containing a run that was cut off mid-drain |
| applied > sent | a run that consumed records it never sent |
| `SweepID` in the fixed-parameter key | two sweeps folded into one table |
| fixed-parameter key | rows that differ in seed, rate, schedule, SHA or broker version |
| `ExactCounts` | net counts presented as exact |

The common shape: **the failures worth guarding against are the ones that
produce a plausible number.** An exception is easy. A table that is confidently
wrong is what actually reaches a decision-maker.

---

## Two limits kept as results

Not everything was fixed. Two were measured, understood, and reported:

**At small balances, a day-count difference is arithmetically indistinguishable
from a rounding difference.** The interest is too small for the ratio to survive
rounding. More classification rules cannot fix it, because the information is
not in the delta.

**Quirks compound.** Twelve are detected in isolation; eleven with all twelve
enabled at once, because some quirks change which transactions land on which
business day and destroy the exact ratios others would show. Reporting only the
compounded number understates what reconciliation can do; reporting only the
isolated number overstates it. Both are in the report.
