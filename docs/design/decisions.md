# Decisions log (append-only)

Mini-ADRs. Newest at the bottom. Never edit an accepted entry; supersede it with a new one.

---

## D-001 — Go for the ledger, Python for simulator/reconciler/reporting

**Status:** accepted (owner, brief §8)
**Context:** The project targets a Go-heavy platform role; the owner's analytical tooling and existing reconciliation code (LedgerLens) are Python.
**Decision:** Go on the hot path and I/O edge (`ledger`, consumer, outbox relay); Python for `legacy-sim`, `reconcile`, report rendering. Protobuf contracts generated for both.
**Consequences:** Two toolchains in `make check`. Contract drift is prevented by checked-in generated code and a CI diff check.

## D-002 — Redpanda as the Kafka-compatible broker

**Status:** accepted (owner, brief §8)
**Context:** Chaos runs need a three-node cluster on a mini PC. Apache Kafka in KRaft mode is viable but heavier; Redpanda is a single binary with the Kafka API.
**Decision:** Redpanda for all compose profiles. Configuration D (Kafka transactions) must be verified against Redpanda at M0; if it diverges, a secondary Apache Kafka compose profile is permitted for D only.
**Consequences:** Findings must state the broker and version. Any Redpanda-specific behaviour is called out in the methods section.

## D-003 — A simulated legacy core, not a real one

**Status:** accepted (owner, brief §2)
**Context:** No real core is available or appropriate. The findings depend on *knowing* the seeded quirks so that detection can be measured.
**Decision:** `legacy-sim` is a deterministic generator with switchable quirks and realistic extract formats (header/detail/trailer, control totals). It is honest about being a simulator.
**Consequences:** Finding 1 measures detection of *known* quirks — a calibration of the twin's reconciliation, not a discovery of unknown ones. `FINDINGS.md` says so in "what this does not prove."

## D-004 — No LLM in any decision path

**Status:** accepted (owner, brief §6)
**Context:** The platform being targeted says "deterministic where it matters." The owner's portfolio stance (LedgerLens, HARBORMASTER) is deterministic-first with bounded AI.
**Decision:** Posting, matching and classification are deterministic and unit-tested. LLM use is limited to optional narrative drafting in the report, flag-off by default and in `make check`.
**Consequences:** No model dependency in the test suite; no API keys required to run `make demo`.

## D-005 — vegeta as the load generator

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 1. k6 has first-class open-model executors but adds JavaScript as a third language to a repo that is otherwise Go + Python. vegeta is Go, matches the ledger toolchain, and keeps `make check` to two toolchains.
**Decision:** vegeta. Open-model arrival is native (`-rate`); the payday, month-end and hot-key profiles of FR-H1 are implemented as a custom `vegeta.Targeter` in Go, driven from the harness.
**Consequences:** Profile shaping is code we own and unit-test, not a k6 script — more work up front, but deterministic and testable inside `make check`, which a JS load script would not be. The HLD must specify the targeter interface and how a profile is seeded so runs stay reproducible. `SETUP.md` §1 still lists k6; it is superseded by this entry.

## D-006 — franz-go as the Go Kafka client

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 2. Configuration D requires Kafka transactions; franz-go supports them and is actively maintained. Sarama's transaction support is thinner, kafka-go has none, confluent-kafka-go pulls in cgo/librdkafka.
**Decision:** franz-go for the outbox relay, the producer and all four consumer delivery modes.
**Consequences:** Pure Go, no cgo, so `go test -race` and the offline `make check` (NFR-8) stay simple. Its transaction API must be verified against Redpanda at M0 per D-002. Verify current APIs via GitHits before coding — the brief may be stale.

## D-007 — Chaos runs target ≥ 1,000 postings/s; steady state keeps ≥ 2,000/s

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 3. Three Redpanda brokers, two Postgres instances and the harness on one Ryzen mini PC is unlikely to hold 2,000 postings/s while brokers are being killed and restarted. A target the box cannot hold produces ablation rows that measure the box, not the consumer design.
**Decision:** NFR-1 (≥ 2,000/s) applies to the single-broker steady-state measurement. New NFR-1a (≥ 1,000/s) applies to chaos and ablation runs. The rate must be *identical* across configurations A–D; the runner refuses to render a table from mismatched artefacts (see `chaos-ablation` skill).
**Consequences:** Finding 2 is a comparison between configurations at a fixed rate, not an absolute throughput claim. `FINDINGS.md` methods must state both rates and why they differ; "what this does not prove" already covers single-box numbers.

## D-008 — Configuration D may slip past weekend 2

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 4. Configurations A (at-most-once), B (naive at-least-once) and C (at-least-once + inbox) already demonstrate loss, duplication and the correct fix with its latency cost. D (Kafka transactions) adds the end-to-end-transactional comparison and carries the Redpanda-divergence risk of D-002.
**Decision:** M6 ships Finding 2 with A, B and C. D is the first item of weekend 3 and is not a weekend-2 exit criterion.
**Consequences:** The execution plan splits M6 into M6 (A–C, Finding 2) and M6b (D, Finding 2 extended). `make report` must render a valid table from three configurations and mark D as `not run` rather than failing. If the interview lands before M6b, `git tag round-2` captures A–C, which is a complete result on its own.

## D-009 — Public repository name `shadowbook`

**Status:** accepted (owner, Phase 0 gate, 2026-09-03)
**Context:** Open question 5.
**Decision:** `github.com/roshanrana/shadowbook`. Go module path follows.
**Consequences:** Module path is fixed before any Go code exists, so no rename churn.

## D-010 — Two demo windows, anchored 2028-02-28 and 2028-10-02

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** Quirk cadences are calendar-gated. A 30-business-day window is ~6 calendar weeks and cannot contain both Columbus Day (Q5, October) and a leap day (Q6, February) — they are ~4.5 months apart. Anchoring naively on Feb 29 2028 also puts the month boundary on Wednesday 1 March 2028, where the calendar first *is* the first business day, so Q12 could not fire either. Left unfixed, Finding 1 reports false negatives that look like reconciliation failures.
**Decision:** `make demo` runs two windows. W1 `leap-and-month-end` = 2028-02-28 → 2028-04-07 (verified: exactly 30 business days; contains Feb 29; contains month ends Feb 29 and Mar 31; 1 April 2028 is a Saturday so Q12 diverges; nearest federal holiday Presidents Day 2028-02-21 falls outside). W2 `columbus` = 2028-10-02 → 2028-10-13 (9 business days — Columbus Day 2028-10-09 is excluded from its own window by the documented calendar, which is the point). Finding 1 gains a `window` column.
**Consequences:** A test asserts every quirk in `quirks.yaml` is reachable by at least one configured window before a run may render, so this defect cannot silently return. `quirks.yaml` is unchanged — the alternative, redefining Q5 to a holiday inside W1, was rejected as a quirk redefinition. HLD §5.5.

## D-011 — `legacy-sim` coverage target ≥ 85%

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** NFR-7 set coverage targets for `ledger` and `reconcile` but none for `legacy-sim`, whose FR-S6 byte-identical determinism the whole of Finding 1 rests on.
**Decision:** `legacy-sim` ≥ 85% lines, enforced in `make check`.
**Consequences:** NFR-7 amended in `01-requirements.md` at Phase 2.

## D-012 — legacy-sim reaches the ledger over both HTTP and the topic

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** FR-S4 left the ingress path open. Finding 1 wants the replay path a real migration would use and something a reviewer can reproduce with `curl`; Finding 2 is *about* the consumer, so postings must cross the broker for loss and duplication to mean anything.
**Decision:** Both. HTTP `POST /postings` with an idempotency key drives `make demo` and Finding 1; the movement topic drives `make ablate` and Finding 2. Both land in one posting service behind a single interface.
**Consequences:** A contract test asserts identical entries, balances and outbox rows for the same input through either path (HLD risk R6). Two ingress paths are a real cost; the test is the control.

## D-013 — Stack: net/http, pgx v5, goose, buf, client_golang, stdlib data handling, Jinja2

**Status:** accepted (owner, Phase 1 gate, 2026-09-03)
**Context:** HLD §7, with registry versions verified 2026-09-03.
**Decision:** HTTP/JSON on `net/http` + stdlib mux (gRPC rejected: vegeta drives HTTP, so it would undo D-005); `jackc/pgx` v5.10.0 (typed `*pgconn.PgError` is what makes idempotency-by-constraint real); `pressly/goose` v3.28.0 with embedded plain SQL (invariants live in reviewable DDL); `buf` with generated code checked in (`buf breaking` guards the frozen contract, and check-in keeps NFR-8 offline); `prometheus/client_golang`; **no pandas or polars in `reconcile`** — stdlib `csv`/`dataclasses`/`decimal` with explicit sort keys, because deterministic ordering (NFR-5) beats speed we do not need; Jinja2 for report rendering.
**Consequences:** Two toolchains, few dependencies, everything mypy- and vet-checkable. Any later wish for a dataframe library in `reconcile` is a decision to re-open here, not a mid-task convenience.

## D-014 — LLD judgement calls accepted: forward-only migrations, recon Postgres, no tracing

**Status:** accepted (owner, Phase 2 gate, 2026-09-03)
**Context:** `03-lld.md` §9 flagged the three decisions most likely to attract disagreement, rather than burying them.
**Decision:** (a) Migrations are forward-only with no down path — the databases are recreated per run and a reversible migration on an append-only table is a fiction. (b) `reconcile` owns its own Postgres rather than emitting files, which buys break ageing across business days cheaply at the cost of a second database in compose. (c) No OpenTelemetry — four processes with structured logs and a shared request ID do not need it, and it would cost `make check` time for no finding.
**Consequences:** Anyone reading the repo who expects down-migrations or traces should read this entry first. If SHADOWBOOK ever outgrows the harness framing, (a) and (c) are the first two to revisit.

## D-015 — Ledger DDL and balance query verified against PostgreSQL 16.13 before approval

**Status:** accepted (2026-09-03)
**Context:** The LLD's schema is the implementation contract for M0 and M1. Schema defects found in Phase 4 cost a whole session under the two-strike rule.
**Decision:** SQL in `03-lld.md` is executed against a real PostgreSQL 16 instance and its invariants exercised before the document is presented for approval. Recorded in `03-lld.md` §8.
**Consequences:** Two defects were caught this way — `entries.entry_id` typed `BIGGSERIAL`, and a balance query missing its mandatory `GROUP BY` that also returned no row for an account with no checkpoint. The same practice applies to any future DDL change: it is executed before it is approved, not after it fails.

## D-016 — A local-only `go.work` supplies GitHub mirrors for vanity import paths

**Status:** accepted (implementation, 2026-09-03)
**Context:** The build environment used for the initial implementation reaches `github.com`, PyPI and npm, but not `proxy.golang.org`, `golang.org`, `gopkg.in` or `google.golang.org`, and no Go module proxy is reachable. Vanity import paths therefore cannot be resolved there. Putting `replace` directives in `go.mod` would ship a repository that is wrong for any normal machine.
**Decision:** `go.mod` stays clean and correct — real vanity paths, real versions. A **gitignored `go.work`** carries eleven `replace` directives mapping each vanity path to its GitHub mirror at the same version. Sums land in `go.work.sum`, also gitignored, so `go.sum` is never polluted.
**Consequences:** On a machine with normal network access, delete `go.work`; nothing else changes. `go get` does **not** honour workspace replaces, so dependencies added in that environment must be written into `go.mod` with `go mod edit -require` and resolved by `go build`. `go.sum` must be regenerated once on a networked machine (`go mod tidy`), and the checksum database re-verified — it was disabled (`GOSUMDB=off`) in the restricted environment, which is a real reduction in supply-chain verification and is why the regeneration is not optional.

## D-017 — Compose images pinned by tag, with a script to convert to digests

**Status:** accepted (implementation, 2026-09-03)
**Context:** T-004 requires every image pinned by digest. No container registry is reachable from the build environment, so digests cannot be resolved there.
**Decision:** Images are pinned to immutable patch tags (`postgres:16.13-bookworm`, `redpandadata/redpanda:v24.3.6`, `prom/prometheus:v3.1.0`) and `scripts/pin-digests.sh` resolves and rewrites them in one pass on a machine with registry access.
**Consequences:** Running that script is a **prerequisite for the public repo** and is listed in the ship report. Until it runs, a tag could in principle be re-pushed; patch tags make that unlikely but not impossible.

## D-018 — A ~60-line embedded migrator replaces goose

**Status:** accepted (implementation, 2026-09-03) — **supersedes the goose half of D-013**
**Context:** goose could not be resolved in the build environment (its dependency graph reaches `go.uber.org`, and `go get` ignores the D-016 workspace replaces). Beyond that constraint: SHADOWBOOK needs "apply numbered embedded SQL files in order, record versions, forward-only, Postgres only", while goose carries drivers for many databases that this project will never use.
**Decision:** `migrations/migrate.go` — `Load` and `Apply` over an `embed.FS`, each migration in its own transaction, versions recorded in `schema_migrations`, re-running a no-op, and a gap in the version sequence is an error.
**Consequences:** Everything the LLD actually specified is unchanged — plain SQL, numbered `NNNN_slug.sql`, forward-only, embedded, applied at start-up. One fewer dependency in a project whose stated value is a small deterministic surface. **Honest caveat:** the proximate cause was an environment limitation, not a design insight; goose remains a defensible choice and reverting is a contained change to one file. Revert if the project ever needs multi-database support or goose's `--no-versioning` fixture mode.

## D-019 — pgx pinned to v5.7.5, not v5.10.0: the approved LLD version was incompatible

**Status:** accepted (implementation, 2026-09-03) — **corrects D-013**
**Context:** D-013 and LLD §7.2 pinned `jackc/pgx` v5.10.0, verified as the latest release on 2026-09-03. Building against it revealed that **v5.10.0 declares `go 1.25.0`**, while the project's own constraint (requirements §5, CLAUDE.md, SETUP.md) is **Go 1.23+**. The two cannot both hold: a machine with Go 1.23 or 1.24 cannot build the approved dependency set.
**Decision:** Pin pgx v5.7.5, which builds on Go 1.23+. The typed `*pgconn.PgError` carrying SQLSTATE and `ConstraintName` — the whole reason D-013 chose pgx — is present and verified working in v5.7.5.
**Consequences:** This was a defect in an approved document, found only by building. The alternative is to raise the project's Go floor to 1.25 and keep v5.10.0; that is a one-line change to `go.mod` and SETUP.md if the owner prefers it. Recommend staying on 1.23+ for now: nothing in the project needs a Go 1.25 feature, and a lower floor is one less thing for a reviewer to install.

## D-020 — franz-go pinned to v1.19.5, and the Kafka clients verified against kfake

**Status:** accepted (implementation, 2026-09-04) — **implements D-006, which had never been built**
**Context:** D-006 chose franz-go and HLD §5 states that the ablation runs against real Redpanda. Neither was true of the code: franz-go was absent from `go.mod`, `cmd/ledger` unconditionally constructed `broker.NewFake()`, and the `internal/broker` package comment claimed franz-go implementations "live alongside and are exercised against real Redpanda". Finding 2 was three pieces short of runnable — client, wiring, orchestration — not one.
**Decision:** Implement `KafkaProducer` (acks=all, idempotent producer, synchronous produce) and `KafkaConsumer` (consumer group, **autocommit disabled**) against franz-go **v1.19.5**, and verify them against `kfake`, which speaks the Kafka wire protocol over real sockets. `cmd/ledger` gains `-brokers`, `-group` and `-topic`; with no seeds it keeps the in-process fake, so the Finding 1 demo still needs no infrastructure.
**Consequences:** Autocommit is the load-bearing setting: the four delivery modes differ only in when `Commit` is called relative to applying an effect, so a background committer on a timer would flatten all four into one measurement. v1.19.5 rather than the current v1.21.6 because the latter declares `go 1.25.0` against this project's 1.23 floor — the same incompatibility D-019 found in pgx; `GroupTransactSession`, which configuration D needs, was verified present in v1.19.5 rather than assumed. Four modules added (`franz-go`, `kmsg`, `kadm`, `kfake`) and `go.mod` now carries no `// indirect` block for them, so the D-016 `go mod tidy` on a networked machine will add those and may raise `golang.org/x/crypto` from v0.31.0 to v0.38.0, which kfake requires. **This does not make Finding 2 measurable**: kfake is a single in-process broker that cannot be killed mid-write. It makes the client code verified rather than hopeful, which is a smaller and different claim.

## D-021 — a run against an in-process broker can never render as Finding 2

**Status:** accepted (implementation, 2026-09-04)
**Context:** The ablation orchestration has to be runnable without Docker or it cannot be developed or tested on most machines. But a smoke run produces an artefact of exactly the same shape as a real one, and its numbers look entirely reasonable. The existing fixed-parameter guard does not catch this: a set of smoke runs is perfectly self-consistent — same seed, rate, schedule, SHA — so it passes and renders a table describing the harness rather than the system.
**Decision:** Artefacts from an in-process broker record a `BrokerVersion` prefixed `fake:`, and `ablation.Table` refuses them **before** any other check, naming the run and saying how to obtain a real measurement.
**Consequences:** A green harness check can never be mistaken for a finding, which matters because nothing about such a table would look wrong. Tests assert both directions — self-consistent fake-broker runs refused, otherwise-identical real-broker runs accepted — so the guard cannot pass for an unrelated reason. The cost is one more thing that must be set correctly when a real cluster is added; `make ablate` supplies it from `-broker-version`.

## D-022 — each ablation run gets its own database AND its own topic

**Status:** accepted (implementation, 2026-09-04)
**Context:** Each run was given a fresh database and a fresh consumer group, which seemed sufficient. It was not, and the smoke test found it: a fresh group starts at the beginning of the topic, so run 2 replayed every record run 1 had produced. The replay hit before the runner had seeded accounts, so the ledger died on a foreign-key violation — a loud failure, but the quiet version is worse: had accounts existed, run 2 would simply have counted run 1's traffic.
**Decision:** Per-run topic (`shadowbook.movements.<run-id>.v1`) alongside the per-run database and group, created explicitly via `broker.EnsureTopic` rather than relying on broker auto-creation. Migrations and account seeding move **before** the ledger starts, since the consumer begins applying the moment it joins.
**Consequences:** Isolation now covers the log as well as the database. Explicit topic creation matters on real Redpanda, where auto-creation is off by default: a run would otherwise produce nothing and report total loss, which is indistinguishable from the result the experiment exists to measure. Relatedly, a produce that fails on the *first* batch is now a hard error rather than a recorded observation — nothing has been killed yet, so it is the experiment failing to start, and reporting it as a run that sent nothing would render as total loss.

## D-023 — A simulated multi-broker cluster, so the ablation is runnable without Docker

**Status:** accepted (implementation, 2026-09-04)
**Context:** The owner has one machine, and no container registry is reachable from the build environment (Docker Hub, ghcr.io, quay.io, public.ecr.aws and mirror.gcr.io were all tested and refused). Redpanda publishes only `rpk` on GitHub releases, not the broker, and the Apache and Maven hosts are blocked too, so Kafka cannot be built either. Without an alternative, Finding 2 stays permanently unmeasured and `make ablate` is a command nobody can run — including any reviewer of this repo.
**Decision:** `ablation.SimCluster` — several `kfake` brokers in one process, each on its own TCP listener. The chaos schedule's `Kill`/`Start` map to `RemoveNode`/`AddNode`, restarting on the **same port**, and each kill also calls `RehashCoordinators`. `make ablate-sim` runs the full sweep with no Docker.
**Consequences:** The comparison is real in the ways that matter: the Kafka wire protocol over real sockets, a separate ledger process with its own offset state, genuine consumer rebalances, identical chaos across configurations. It is **not** Redpanda: no replication, no ISR, no unclean leader election, no disk, so absolute latency and throughput describe one process on one machine. Artefacts record `BrokerVersion` prefixed `sim:` and the report labels the whole section accordingly (D-024). The coordinator rehash is not decoration: without it a kill never forces a rebalance, and the first simulated sweep produced three near-identical rows — an ablation that silently measured nothing.

## D-024 — Three kinds of run, and a table may not mix them

**Status:** accepted (implementation, 2026-09-04) — **extends D-021**
**Context:** With a simulated cluster there are now three kinds of artefact, and the two-way plumbing/real split of D-021 is too coarse to describe them.
**Decision:** `Artefact.Kind()` returns `plumbing` (single in-process broker, `fake:` — refused outright), `simulated` (`sim:` — rendered, under its own labelled heading) or `real`. `KindOf` refuses a set that mixes kinds rather than degrading to the weakest, because rows of different kinds are not comparable to each other, which is the one property an ablation table must have.
**Consequences:** A simulated result can be published honestly instead of being suppressed or overclaimed. The report states in the section itself what the cluster was and what its numbers may be used for.

## D-025 — Retriable fetch errors must not kill the ledger

**Status:** accepted (implementation, 2026-09-04) — **defect found by the first simulated ablation**
**Context:** `KafkaConsumer.Poll` treated every fetch error as fatal. Under a broker kill, franz-go reports `ErrDataLoss` when a partition's leader epoch resets; the consumer returned it, `errgroup` cancelled, and **the ledger process exited**. The first simulated sweep therefore measured how long the ledger survived rather than how the delivery modes behaved: a loss column that did not correlate with the configuration at all, the same mode losing everything in one run and nothing in the next.
**Decision:** Classify fetch errors. Poll deadlines and caller cancellation are the quiet case; retriable broker errors and connection failures are transient and consuming continues; `ErrDataLoss` is **counted** and consuming continues. Only unclassified errors are fatal.
**Consequences:** The ledger now rides out broker loss, which is the behaviour its own requirements assume. Exiting was the worst available response: it silently converted a recoverable backlog into permanent loss, because a dead consumer applies nothing. Data loss is counted rather than swallowed — it means the cluster dropped records the consumer had not read, which is exactly the event being measured, so it belongs in the artefact rather than being inferred from a shortfall.

## D-026 — Loss and duplication are exact only where there is an inbox

**Status:** accepted (implementation, 2026-09-04)
**Context:** The first measurement counted `duplicated` as `postings − applied`, which is identically zero for modes A and B: neither keeps an inbox, and both mint a fresh posting id per delivery, so nothing in the ledger ties an effect back to a movement. Mode B was observed applying 26,221 effects for 12,000 movements and reporting **zero** duplicates.
**Decision:** For C and D, `Applied` comes from the inbox and the counts are exact. For A and B they are **net**: `Duplicated = max(0, effects − sent)` and `Lost = max(0, sent − effects)`. Every artefact carries `ExactCounts`, and the report states the distinction wherever the figures are net.
**Consequences:** A run that lost 100 movements and applied 100 others twice is indistinguishable from a clean one under A and B. That is not a harness limitation to apologise for but the operational consequence of running without an inbox: without one the ledger cannot answer "was this applied twice?" at all — which is itself an argument for configuration C.

## D-027 — `go.sum` exists, and the Go floor rose to 1.23.8

**Status:** accepted (implementation, 2026-09-04) — **discharges the D-016 obligation**
**Context:** D-016 left `go.sum` ungenerated because no module proxy was reachable from the build environment, and recorded that regenerating it was not optional: the tree had been built with `GOSUMDB=off`, a real reduction in supply-chain verification. `go mod tidy` has now run on a networked machine with the checksum database enabled.
**Decision:** Commit the generated `go.sum` (93 entries) and the `go.mod` that `tidy` produced.
**Consequences:** Three things changed that were not asked for and are worth stating rather than absorbing:
1. **The Go floor moved from `1.23` to `1.23.8`**, because `franz-go/pkg/kfake` and `pkg/kadm` declare it. SETUP.md and CLAUDE.md are updated to match. This is the third time a dependency has moved the floor (D-019 pgx, D-020 franz-go), and it is the mechanism to watch: the floor is set by the strictest dependency, not by the project.
2. `golang.org/x/crypto` rose v0.31.0 → v0.38.0, as D-020 predicted, along with `x/sync`, `x/sys` and `x/text`. All are forward moves.
3. The `// indirect` block now exists, which `go.mod` had been missing entirely.
Verified after adopting the tidied module: `go build ./...`, `golangci-lint` (0 issues) and the full unit suite all pass. `scripts/dev-workspace.sh` needed two fixes to cope — it hard-coded `go 1.23` in the generated workspace, which Go rejects outright when the module requires more, and its error extractor matched a fixed list of vanity hosts and broke on the first new one (`gonum.org`, via vegeta's tdigest). Both are now derived rather than hard-coded.

## D-028 — Credentials have one source of truth, and it is `.env`

**Status:** accepted (implementation, 2026-09-04) — **defect in the documented setup path**
**Context:** `docker-compose.yml` passes `${LEDGER_DB_PASSWORD}` to Postgres and refuses to start without it; `.env.example` ships that variable as `change-me`; the `Makefile` hardcoded `postgres://shadowbook:shadowbook@...`. The documented path in SETUP.md — `cp .env.example .env`, `make up`, `make check` — therefore fails password authentication on every fresh clone. It was found the first time anyone brought the stack up from the documented instructions rather than from a script.
**Decision:** The Makefile loads `.env` and builds both DSNs from the same variables compose uses, with `shadowbook` as the fallback when no `.env` exists (which keeps CI and the sandbox working unchanged).
**Consequences:** One source of truth for the credentials. The reason this survived so long is worth recording: every environment the project had run in created PostgreSQL by some other means — a sandbox script that set the password to `shadowbook` directly, and CI service containers — so the hardcoded string happened to be correct everywhere except the one path the documentation describes. A setup step that only ever runs in environments it was not written for is not evidence that it works.
