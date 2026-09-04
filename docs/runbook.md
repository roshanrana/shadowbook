# SHADOWBOOK — runbook

Everything an operator or a reviewer needs to run this, and what to do when it
misbehaves. Written before ship, per the conventions in `CLAUDE.md`.

## Prerequisites

| Tool | Version | Check |
|---|---|---|
| Go | 1.23+ | `go version` |
| uv | latest | `uv --version` |
| Docker + compose v2 | any current | `docker compose version` |
| protoc + protoc-gen-go | 29.x / v1.36.x | `protoc --version` |
| golangci-lint | v2.5+ | `golangci-lint --version` |

`make check` runs offline. Only `make up` and the ablation runs need Docker.

## First run

```
cp .env.example .env      # set LEDGER_DB_PASSWORD and RECON_DB_PASSWORD
make check                # the gate; must be green before anything else
make demo                 # Finding 1, both windows, regenerates reports/FINDINGS.md
```

`make demo` needs no containers: it runs the simulator and the reconciler and
writes `reports/FINDINGS.md`. It takes a couple of seconds.

## Running the ledger

```
make up                   # one broker, two Postgres, Prometheus
go run ./cmd/ledger       # migrations apply at start-up
curl -s localhost:8080/readyz
make down
```

Ports: ledger DB 5433, recon DB 5434, Redpanda 9092, Prometheus 9090.

## Running the ablation (Finding 2)

```
make up-chaos             # three brokers, RF=3, min.insync.replicas=2
make ablate               # configurations A-C, three runs each
make report               # regenerates FINDINGS.md with Finding 2 populated
make down
```

Configuration D is M6b and is not in the default set (D-008). `make report`
renders a valid document with Finding 2 marked **not run** — that is expected,
not a failure.

## Reading the output

`reports/FINDINGS.md` is generated. Do not edit it; edit
`report/templates/FINDINGS.md.j2` and re-run `make report`.

Run artefacts live under `reports/runs/`. They are the only input to
`make report`, which never touches a live system — an old artefact renders the
same document today as it did when it was written.

## When something is wrong

**`make check` fails on `golden-check`.**
`internal/bizdate` and `legacy_sim.calendar` have drifted. That is the control
for HLD risk R7 and it is doing its job. Fix whichever side is wrong, then
`make golden-calendar` to regenerate the fixture. Regenerating without fixing
the drift defeats the test.

**`make check` fails on `gen-check`.**
A `.proto` changed without `make proto`. Run it and commit `gen/`.

**`UnreachableQuirkError` from the simulator.**
An enabled quirk cannot fire in any configured window, so Finding 1 would report
it as undetected for calendar reasons. See `decisions.md` D-010. Either add a
window that reaches it or disable the quirk deliberately.

**A quirk reports as undetected in `FINDINGS.md`.**
Undetected rows are shown on purpose and are the most interesting rows in the
table. Before assuming the reconciler is at fault, check in this order:
1. Can the quirk fire in the window at all? (`reconcile.finding1` runs the
   reachability guard.)
2. Does the simulator actually exercise it? Most historical false negatives were
   simulator bugs — a transaction dropped instead of carried, a business day
   never simulated, a balance snapshot taken at the wrong hour.
3. Is there a classification rule whose signature maps to it?
   (`reconcile/discovery.py`.)

**Integration tests skip.**
`SHADOWBOOK_LEDGER_DSN` is unset. `make up` first, or export it by hand.

**Postgres is up but migrations fail.**
Migrations are forward-only with no down path (D-014). Recreate the database:
`make down && make up`. The databases are disposable by design.

**The ledger will not start: `unrecognized import path`.**
The build machine cannot reach a Go module proxy. That is what `go.work` is for
in restricted environments (D-016) — but on a normal machine `go.work` should
NOT exist. Delete it and run `go mod tidy`.

## Before making the repository public

```
scripts/pin-digests.sh          # D-017: compose images to digests
govulncheck ./...
uv run pip-audit
gitleaks detect                 # or git secrets --scan
grep -rn "password" --include="*.yml" .    # nothing but ${VAR:-...} defaults
go mod tidy                     # regenerate go.sum with GOSUMDB on (D-016)
```

The last one is not optional: `go.sum` was generated with the checksum database
disabled in a restricted environment, and must be regenerated somewhere it is
reachable.

## Finding 2 against a real Redpanda cluster

`make ablate-sim` needs nothing but Go and PostgreSQL, and produces a result
labelled *simulated*. Producing the real Finding 2 needs a machine that can pull
container images. Only two things are required there: **Docker** and **Go** —
not `make`, not `uv`, so the commands below work in PowerShell as written.

```
cd <repo>

# 1. Fetch dependencies and generate go.sum. Also discharges D-016.
go mod tidy

# 2. Three brokers with replication factor 3, plus both PostgreSQL instances.
docker compose --profile chaos up -d
docker ps --format "{{.Names}}"      # expect redpanda-1, redpanda-2, redpanda-3

# 3. Build the ledger. The runner starts it as a separate process per run.
go build -o bin/ledger ./cmd/ledger          # bin/ledger.exe on Windows

# 4. The sweep: 3 configurations x 3 runs, ~4 minutes of load each.
go run ./cmd/harness ablate --runs 3 --out reports/runs/redpanda ^
  --dsn "postgres://shadowbook:shadowbook@localhost:5433/ledger?sslmode=disable" ^
  --ledger bin/ledger --broker-version "redpanda v24.3.6"

# 5. Fold the artefacts into the report's input.
go run ./cmd/harness fold --in reports/runs/redpanda --out reports/runs/redpanda/finding2.json
```

Notes that have already cost time once each:

- **Write to a NEW directory.** A table may not mix simulated and real runs, and
  `reports/runs/demo` already holds simulated ones. The runner now refuses this
  up front rather than after the sweep.
- **Container names are pinned in compose.** The chaos schedule kills brokers by
  name (`redpanda-1`), and Compose would otherwise generate
  `<project>-redpanda-1-1`. A failed kill is recorded rather than fatal, so
  without the pin the sweep runs with no chaos at all and reports three
  identical rows.
- **Topics are created with replication factor 3** on a real cluster, from
  `Cluster.Replicas()`. The chaos profile sets `minimum_topic_replications=3`;
  an RF=1 topic makes a broker kill destroy partitions instead of failing over,
  which reads as a dramatic result rather than a misconfiguration.
- The run is done when `fold` writes `finding2.json`. `make report` then renders
  it, and the "simulated cluster" banner disappears on its own because the
  artefacts' broker version no longer carries the `sim:` prefix.
