---
name: chaos-ablation
description: How to run the SHADOWBOOK delivery-semantics ablation (configurations A–D) and broker-kill chaos reproducibly, and exactly what to record so make report can render Finding 2. Read this whenever a task touches the harness, compose chaos profiles, or run artefacts.
---

# Chaos and ablation runs

## Fixed across every configuration

Seed, load profile, duration, chaos schedule, broker count (3), producer durability (`acks=all`, RF=3, `min.insync.replicas=2`, unclean leader election off), ledger version (git SHA), broker version. If any differ between rows, the run is invalid — the runner refuses to render a table from mismatched artefacts.

## Default schedule

    t+0s     start load (profile: payday)
    t+60s    docker kill <broker-1>
    t+90s    docker start <broker-1>
    t+150s   docker kill <broker-2>
    t+180s   docker start <broker-2>
    t+240s   stop load; drain until consumer lag == 0 or 120 s timeout

## Record per run — one JSON artefact under `reports/runs/<ts>-<config>.json`

- `config` (A|B|C|D), `seed`, `profile`, `git_sha`, `broker_version`, `started_at`
- `sent`, `applied`, `lost` (= sent − distinct applied), `duplicated` (= applied − distinct applied)
- p50/p95/p99 latency from an **open-model** generator (document the generator; coordinated omission matters)
- `consumer_lag_peak`, `drain_seconds`
- `invariant_ok`; if false, `invariant_first_violation_at`, `invariant_delta_minor`
- Chaos events actually executed, with timestamps — what happened, not the plan

## Rules

- Never hand-edit an artefact. Bad run → delete and re-run; say why in the run log.
- One config per run; never interleave.
- ≥ 3 runs per config; the report shows median and range. One run is an anecdote.
- Loss and duplication are counted at the **ledger** from entries, not from client counters. Client view recorded separately as `client_acked`.
- Config D: verify the broker supports the transactional API used; otherwise the row reads `not run — <reason>`. Never guess.

## Interpreting

A: loss, no duplicates. B: duplicates, no loss. C: neither, at a latency cost. D: neither within Kafka; methods must say where the guarantee ends at the DB boundary. If B shows no duplicates, the kill did not land during in-flight processing — increase load or shorten the commit interval. Do not report a null result as a finding.
