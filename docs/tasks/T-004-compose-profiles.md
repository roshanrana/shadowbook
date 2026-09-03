# T-004 — Compose profiles, pinned images, Prometheus scrape
Status: todo      Milestone: M-G   Wave: 2
Depends on: T-001

## Goal
`make up` brings up one Redpanda, two Postgres and Prometheus; `make up-chaos` brings up three Redpanda brokers with the durability settings NFR-9 fixes. Every image is pinned by digest so `make check` stays offline-reproducible.

## Context
- `docker-compose.yml` and `scripts/prometheus.yml` already exist from the Phase 0 scaffold — read both before editing.
- NFR-9 is **fixed for every run and asserted by a test later**: `acks=all`, RF=3, `min.insync.replicas=2`, unclean leader election off. Set the broker-side halves here (RF and min.insync.replicas defaults, unclean election disabled); the producer-side halves live in T-013.
- Two Postgres instances, not two schemas (LLD §5.1): ledger on 5433, recon on 5434.
- D-002: Redpanda for all profiles. Note the exact version and digest in handoff — `FINDINGS.md` must state the broker version.
- SETUP.md warns the box may struggle with the chaos profile; record observed memory in handoff, it feeds D-007.

## Contracts to honor
Topic names and partition counts from LLD §4.2: `shadowbook.postings.v1` and `shadowbook.movements.v1`, key `account_id`, 6 partitions each. Ports: ledger DB 5433, recon DB 5434, Prometheus 9090.

## File scope
Modify: `docker-compose.yml`, `scripts/prometheus.yml`, `.env.example`, `Makefile` (only the `up`, `up-chaos`, `down` targets).

## Suggested steps
1. Read the existing compose file; keep its profile names (`single`, `chaos`).
2. Pin every image by `name@sha256:…`, not by tag.
3. Configure the chaos profile's three brokers with RF 3 and `min.insync.replicas=2`; disable unclean leader election.
4. Add both Postgres services with distinct volumes and ports; healthchecks on both.
5. Point Prometheus at the ledger's `/metrics` and set a 1 s scrape interval (NFR-3 measures invariant lag ≤ 1 s).
6. Bring both profiles up and down once; record startup time and peak memory.

## Acceptance criteria
- [ ] `make up` then `make down` completes cleanly, leaving no volume behind (`-v` is already in the target)
- [ ] `make up-chaos` starts three brokers that form a cluster (verify with `rpk cluster info` or the admin API)
- [ ] Every image reference contains `@sha256:`
- [ ] Both Postgres instances accept connections on 5433 and 5434 with credentials from `.env`
- [ ] `.env.example` documents every variable the compose file reads, with no real password
- [ ] Peak memory of the chaos profile recorded in handoff notes

## Validation
```
docker compose --profile single config     # must parse
docker compose --profile chaos  config
make up && make down
make up-chaos && make down
```

## Out of scope
Producer configuration (T-013). Any Go or Python code. Grafana or dashboards — out of scope for the whole project.

## Handoff notes
_(filled by the worker)_
