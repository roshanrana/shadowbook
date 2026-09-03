# Low-Level Design — SHADOWBOOK

Status: **approved 2026-09-03** — interfaces in §4 are now FROZEN
HLD: `docs/design/02-hld.md` (approved 2026-09-03) · Requirements: `docs/design/01-requirements.md` · Decisions: `docs/design/decisions.md`

> Interfaces in §4 are **frozen contracts** on approval. Changing one afterwards is an LLD change: update this document, list every affected task pack, get sign-off, then propagate. It is not a mid-task decision.

## 1. Repository layout

Phase 4 scaffolds exactly this. Directories not listed here do not get created.

```
shadowbook/
├── cmd/
│   ├── ledger/main.go              # ledger process: API + relay + consumer + EOD
│   └── harness/main.go             # load, chaos, ablation runner
├── internal/
│   ├── money/                      # Amount{minor int64, currency, scale}. THE money type.
│   ├── bizdate/                    # business date, calendar, cut-off, day-count, rounding
│   ├── ledger/
│   │   ├── posting/                # idempotency, zero-sum, entry writes  (FR-L1..L3)
│   │   ├── balance/                # derived balances, checkpoints, holds (FR-L4, L5)
│   │   ├── accrual/                # EOD interest + fees                  (FR-L6)
│   │   ├── outbox/                 # relay to Redpanda                    (FR-L7)
│   │   ├── consumer/               # four delivery modes                  (FR-L8)
│   │   ├── httpapi/                # handlers, decoding, error mapping    (FR-L1)
│   │   ├── store/                  # pgx queries + embedded migrations
│   │   └── obs/                    # metrics, invariant checker           (FR-L9)
│   └── harness/
│       ├── load/                   # vegeta Pacers + Targeters            (FR-H1)
│       ├── chaos/                  # docker kill/start schedule           (FR-H2)
│       └── ablation/               # runner + run artefacts               (FR-H3)
├── migrations/                     # goose plain-SQL, embedded via embed.FS
├── contracts/
│   ├── buf.yaml  buf.gen.yaml  buf.lock
│   └── shadowbook/v1/{posting,movement,extract}.proto
├── gen/go/…  gen/python/…          # generated, CHECKED IN, CI diffs it
├── legacy-sim/
│   ├── pyproject.toml  quirks.yaml
│   ├── src/legacy_sim/{calendar,generator,quirks,extracts,emit}.py
│   └── tests/
├── reconcile/
│   ├── pyproject.toml
│   ├── src/reconcile/{ingest,grains,classify,age,discovery,store}.py
│   └── tests/
├── report/
│   ├── pyproject.toml
│   ├── src/report/render.py  templates/FINDINGS.md.j2
│   └── tests/
├── pyproject.toml                  # uv workspace root: members = the three above
├── go.mod  go.sum
├── Makefile  docker-compose.yml  .env.example
├── docs/{design,tasks}/  reports/{runs}/  scripts/
└── .github/workflows/check.yml
```

Python is a **uv workspace** with three members, not one package: `legacy-sim`, `reconcile` and `report` have genuinely different dependency sets (only `report` needs Jinja2), and the workspace keeps one lockfile and one `uv run` invocation for `make check`.

## 2. Conventions

| Area | Rule |
|---|---|
| Go packages | Lower-case, single word, no `utils`/`common`/`helpers`. `internal/` for everything not in `cmd/`. |
| Go errors | Sentinel errors per package (`var ErrIdempotencyBodyMismatch = errors.New(...)`), wrapped with `%w`. HTTP mapping lives only in `httpapi`. |
| Go concurrency | No goroutine without a cancellation path; every blocking select has `ctx.Done()`; fan-out via `errgroup`. |
| Money | `money.Amount` only. A bare `int64` amount crossing a package boundary is a review failure. No float, anywhere, in either language. |
| Dates | `bizdate.BusinessDate` (a `civil` date) is never a `time.Time`. Wall-clock instants are `time.Time` and only ever come from the injected clock. |
| Python | `src/` layout, `from __future__ import annotations`, `@dataclass(frozen=True, slots=True)` for value types, `Decimal` never `float`, explicit `sorted(..., key=...)` on anything that reaches an output. |
| SQL | Migrations are plain SQL, forward-only, numbered `NNNN_slug.sql`. Table and column names `snake_case`, singular table names avoided (`entries`, not `entry`). |
| Commits | `T-NNN: imperative summary`. One task per commit where practical. |
| Lint/format | `gofmt` + `golangci-lint`; `ruff format` + `ruff check` + `mypy --strict`. Configs at repo root, one per language. |

## 3. Component designs

### 3.1 `internal/money` — the money type

```go
type Currency string // ISO 4217, upper-case

type Amount struct {
    Minor    int64    // signed, in minor units
    Currency Currency
    Scale    uint8    // 2 for USD, 0 for JPY
}

func New(minor int64, c Currency) (Amount, error) // scale looked up from the registry
func (a Amount) Add(b Amount) (Amount, error)     // ErrCurrencyMismatch
func (a Amount) Neg() Amount
func (a Amount) IsZero() bool
```

Scale comes from a compile-time registry, not from the caller: `USD→2`, `EUR→2`, `JPY→0`. `New` rejects an unknown currency. This is what makes Q10 (`jpy_two_decimals_truncated`) structurally impossible on the shadow side — there is no code path that can store JPY at scale 2 (FR-L11).

**Must not:** parse or format for display; do arithmetic across currencies; expose `Minor` for mutation.

### 3.2 `internal/bizdate` — calendar, cut-off, day count, rounding

The single place HLD §5.4's seven rules exist in Go. `legacy_sim.calendar` is its Python mirror, and a golden test asserts the two agree over the full 2027–2029 range.

```go
type BusinessDate struct{ Y int; M time.Month; D int }

type Calendar interface {
    IsBusinessDay(BusinessDate) bool
    NextBusinessDay(BusinessDate) BusinessDate
    FirstOfMonth(y int, m time.Month) BusinessDate          // calendar first (documented)
    FirstBusinessDayOfMonth(y int, m time.Month) BusinessDate
}

// Cut-off: 17:00:00.000 EXCLUSIVE (FR-L12). t < cutoff  => today; t >= cutoff => next business day.
func (c Calendar) BusinessDateFor(t time.Time) BusinessDate

type Basis uint8
const ( ACT365 Basis = iota; ACTACT )

// Documented: ACT/365 all products; ACT/ACT in leap years. Q3 and Q6 diverge from this.
func DayCountFraction(from, to BusinessDate, b Basis) (num, den int64)

// Documented: half-even to minor units. Q1 diverges.
func RoundHalfEven(numerator, denominator int64) int64
```

Holidays are a static table for 2027–2029: New Year's Day, MLK, Presidents, Memorial, Juneteenth, Independence, Labor, **Columbus (documented: holiday — Q5 diverges)**, Veterans, Thanksgiving, Christmas, with the observed-day rule for weekend falls.

**Test plan:** table-driven over every boundary — 16:59:59.999 vs 17:00:00.000 vs 17:00:00.001; Feb 28/29 2028; Mar 1 2028 (Wednesday, business day) vs Apr 1 2028 (Saturday); Columbus Day 2028-10-09; each rounding tie in both directions.

### 3.3 `internal/ledger/store` — schema

Migrations `0001_core.sql` … `0006_inbox.sql`, embedded, applied by goose at start-up.

```sql
CREATE TABLE accounts (
    account_id    UUID PRIMARY KEY,
    product_code  TEXT        NOT NULL,
    currency      CHAR(3)     NOT NULL,
    opened_on     DATE        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE postings (
    posting_id          UUID PRIMARY KEY,
    principal           TEXT        NOT NULL,
    kind                TEXT        NOT NULL
        CHECK (kind IN ('transfer','interest','fee','reversal')),
    currency            CHAR(3)     NOT NULL,
    business_date       DATE        NOT NULL,
    value_date          DATE        NOT NULL,
    posted_at           TIMESTAMPTZ NOT NULL,
    reverses_posting_id UUID        NULL REFERENCES postings(posting_id)
);

CREATE TABLE entries (
    entry_id          BIGSERIAL PRIMARY KEY,
    posting_id        UUID        NOT NULL REFERENCES postings(posting_id),
    account_id        UUID        NOT NULL REFERENCES accounts(account_id),
    currency          CHAR(3)     NOT NULL,
    amount_minor      BIGINT      NOT NULL,
    scale             SMALLINT    NOT NULL,
    business_date     DATE        NOT NULL,
    value_date        DATE        NOT NULL,
    reverses_entry_id BIGINT      NULL REFERENCES entries(entry_id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX entries_account_bdate ON entries (account_id, business_date, entry_id);
CREATE INDEX entries_posting       ON entries (posting_id);
CREATE INDEX entries_bdate         ON entries (business_date);

CREATE TABLE idempotency_keys (
    principal  TEXT        NOT NULL,
    idem_key   TEXT        NOT NULL,
    body_hash  BYTEA       NOT NULL,
    posting_id UUID        NULL REFERENCES postings(posting_id),
    response   JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (principal, idem_key)
);

CREATE TABLE holds (
    hold_id      UUID PRIMARY KEY,
    account_id   UUID        NOT NULL REFERENCES accounts(account_id),
    currency     CHAR(3)     NOT NULL,
    amount_minor BIGINT      NOT NULL CHECK (amount_minor > 0),
    placed_at    TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,          -- placed_at + 72h  (FR-L5)
    released_at  TIMESTAMPTZ NULL,
    release_kind TEXT        NULL CHECK (release_kind IN ('captured','cancelled','expired'))
);
CREATE INDEX holds_open ON holds (account_id) WHERE released_at IS NULL;

CREATE TABLE checkpoints (
    account_id    UUID     NOT NULL REFERENCES accounts(account_id),
    business_date DATE     NOT NULL,
    currency      CHAR(3)  NOT NULL,
    balance_minor BIGINT   NOT NULL,
    last_entry_id BIGINT   NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, business_date)
);

CREATE TABLE outbox (
    outbox_id     BIGSERIAL PRIMARY KEY,
    posting_id    UUID        NOT NULL REFERENCES postings(posting_id),
    partition_key TEXT        NOT NULL,           -- account_id (FR-L7)
    payload       BYTEA       NOT NULL,           -- protobuf PostingEvent
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at       TIMESTAMPTZ NULL
);
CREATE INDEX outbox_unsent ON outbox (outbox_id) WHERE sent_at IS NULL;

CREATE TABLE inbox (
    message_id  TEXT PRIMARY KEY,                 -- MovementEvent.message_id
    topic       TEXT   NOT NULL,
    partition   INT    NOT NULL,
    msg_offset  BIGINT NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**The three invariants that live in DDL, not in Go:**

```sql
-- 1. Append-only (FR-L3). Statement-level so a bulk UPDATE cannot slip through.
CREATE FUNCTION deny_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'SHADOWBOOK: % is append-only', TG_TABLE_NAME; END $$;

CREATE TRIGGER entries_append_only     BEFORE UPDATE OR DELETE ON entries
    FOR EACH STATEMENT EXECUTE FUNCTION deny_mutation();
CREATE TRIGGER checkpoints_append_only BEFORE UPDATE OR DELETE ON checkpoints
    FOR EACH STATEMENT EXECUTE FUNCTION deny_mutation();

-- 2. Zero-sum per posting (FR-L2), deferred so a multi-entry insert is legal mid-transaction.
CREATE FUNCTION assert_posting_zero_sum() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n int; s bigint;
BEGIN
    SELECT count(*), coalesce(sum(amount_minor),0) INTO n, s
      FROM entries WHERE posting_id = NEW.posting_id;
    IF n < 2 THEN RAISE EXCEPTION 'SHADOWBOOK: posting % has % entries, need >= 2',
        NEW.posting_id, n; END IF;
    IF s <> 0 THEN RAISE EXCEPTION 'SHADOWBOOK: posting % sums to %, need 0',
        NEW.posting_id, s; END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER entries_zero_sum AFTER INSERT ON entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_posting_zero_sum();

-- 3. Idempotency is the PRIMARY KEY on (principal, idem_key). There is no other mechanism.
```

**Derived balance (FR-L4)** — never a stored column:

```sql
SELECT coalesce(c.balance_minor, 0) + coalesce(sum(e.amount_minor), 0) AS ledger_minor
FROM       (SELECT 1) AS anchor
LEFT JOIN LATERAL (SELECT balance_minor, last_entry_id FROM checkpoints
                   WHERE account_id = $1 AND business_date <= $2
                   ORDER BY business_date DESC LIMIT 1) c ON true
LEFT JOIN  entries e ON e.account_id = $1
                    AND e.entry_id > coalesce(c.last_entry_id, 0)
                    AND e.business_date <= $2
GROUP BY c.balance_minor;
```

`available = ledger − SUM(open holds not yet expired)`; `pending = SUM(open holds)`.

The `LEFT JOIN LATERAL` and the two `coalesce`s are not decoration. A plain join drops the account entirely when no checkpoint exists yet, and `GROUP BY` is mandatory because `balance_minor` sits beside an aggregate. Verified against PostgreSQL 16.13 for three cases: checkpoint + entries, entries with no checkpoint, and an account with neither (returns one row, `0`).

**Global invariant (FR-L9)**, run on a ticker:

```sql
SELECT currency, sum(amount_minor) AS drift FROM entries GROUP BY currency;
```

Every row must have `drift = 0`. Exposed as `shadowbook_ledger_invariant_ok` and exported as `obs.CheckInvariant(ctx) error` so integration tests assert it directly rather than scraping.

### 3.4 `internal/ledger/httpapi` — the posting API (FR-L1)

Every request carries `X-Principal`. Mutating requests carry `Idempotency-Key`.

| Method | Path | Request | Success | Errors |
|---|---|---|---|---|
| POST | `/v1/postings` | `PostingRequest` | 201 `PostingResponse` | 400, 409, 422, 500 |
| POST | `/v1/accounts/{account_id}/holds` | `HoldRequest` | 201 `HoldResponse` | 400, 404, 409, 422 |
| DELETE | `/v1/holds/{hold_id}` | `{release_kind}` | 200 `HoldResponse` | 404, 409 |
| GET | `/v1/accounts/{account_id}/balances?as_of=YYYY-MM-DD` | — | 200 `BalanceResponse` | 400, 404 |
| GET | `/v1/accounts/{account_id}/entries?business_date=YYYY-MM-DD` | — | 200 `EntriesResponse` | 400, 404 |
| POST | `/internal/eod` | `{business_date}` | 200 `EODResponse` | 400, 409 |
| GET | `/healthz` `/readyz` `/metrics` | — | 200 | — |

```jsonc
// PostingRequest — amounts are integer minor units, always (FR-L11)
{
  "kind": "transfer",                    // transfer | interest | fee | reversal
  "currency": "USD",
  "business_date": "2028-02-29",         // explicit, never inferred (FR-L12)
  "value_date": "2028-02-29",
  "posted_at": "2028-02-29T16:59:59.999Z",
  "reverses_posting_id": null,
  "entries": [                            // >= 2, must sum to zero
    {"account_id": "…", "amount_minor": -125000},
    {"account_id": "…", "amount_minor":  125000}
  ]
}
// PostingResponse
{"posting_id": "…", "business_date": "2028-02-29",
 "entries": [{"entry_id": 4711, "account_id": "…", "amount_minor": -125000}]}

// BalanceResponse — the three balances FR-L4 requires
{"account_id": "…", "currency": "USD", "scale": 2, "as_of": "2028-02-29",
 "ledger_minor": 730050, "available_minor": 680050, "pending_minor": 50000}
```

**Error taxonomy.** `httpapi` is the only package that knows about HTTP status codes.

| Code | HTTP | Cause | Retryable? |
|---|---|---|---|
| `InvalidRequest` | 400 | malformed JSON, bad date, unknown currency | no |
| `MissingIdempotencyKey` | 400 | mutating request without the header | no |
| `UnknownPrincipal` | 403 | principal not in the allow-list | no |
| `AccountNotFound` | 404 | — | no |
| `IdempotencyBodyMismatch` | 409 | same `(principal, key)`, different `body_hash` | no |
| `HoldAlreadyReleased` | 409 | double release | no |
| `EODAlreadyRun` | 409 | EOD replayed for a business date | no |
| `EntriesNotBalanced` | 422 | entries do not sum to zero, or fewer than 2 | no |
| `CurrencyMismatch` | 422 | entry currency ≠ posting currency | no |
| `InsufficientAvailable` | 422 | hold exceeds available balance | no |
| `Internal` | 500 | unexpected | yes |

Envelope: `{"error": {"code": "IdempotencyBodyMismatch", "message": "…", "request_id": "…"}}`.

### 3.5 `internal/ledger/posting` — the posting path

```go
type Service interface {
    Post(ctx context.Context, p Request) (Result, error)     // used by BOTH ingress paths (D-012)
    Reverse(ctx context.Context, postingID uuid.UUID, principal string, key string) (Result, error)
}
```

Algorithm, exactly as HLD §6.1:

1. `BEGIN` (read committed), `SET CONSTRAINTS ALL DEFERRED`.
2. `INSERT INTO idempotency_keys (principal, idem_key, body_hash, response) VALUES (…)`.
3. On `23505` from `idempotency_keys_pkey`: `ROLLBACK`, `SELECT body_hash, response`. Equal hash → return the stored response verbatim. Different hash → `ErrIdempotencyBodyMismatch`.
4. `INSERT INTO postings`, then `INSERT INTO entries` (all of them).
5. `INSERT INTO outbox` with `partition_key = entries[0].account_id`, payload = marshalled `PostingEvent`.
6. `UPDATE idempotency_keys SET posting_id, response` — permitted: `idempotency_keys` is not append-only.
7. `COMMIT`. The deferred zero-sum trigger fires here; a violation surfaces as `EntriesNotBalanced`.

The duplicate is found by the constraint violation, never by a prior `SELECT` (CLAUDE.md). The named adversarial scenario "N concurrent same-key requests → one effect" is a test that runs 64 goroutines against one key and asserts exactly one posting row, one outbox row, and 64 identical responses.

**Body hash:** SHA-256 over the canonical JSON encoding (sorted keys, no insignificant whitespace) of the request body, excluding the `Idempotency-Key` header itself.

### 3.6 `internal/ledger/accrual` — EOD (FR-L6)

Triggered by `POST /internal/eod`, never by wall-clock time (determinism, HLD §8.3). Idempotent per business date via a unique row in a small `eod_runs` table; a replay returns `EODAlreadyRun`.

Order within a business date is fixed and tested: **hold expiry → interest accrual → fee assessment → checkpoint**. Fees must see post-expiry available balance, which is exactly what Q7 (`min_balance_fee_on_ledger_balance`) diverges on.

```go
func Accrue(ctx, cal bizdate.Calendar, d bizdate.BusinessDate) error
// interest_minor = RoundHalfEven(principal_minor * rate_bp * num, 10_000 * den)
// (num, den) = bizdate.DayCountFraction(prev, d, ACT365 | ACTACT in a leap year)
```

Interest posts on the **calendar** first of the month (documented); Q12 diverges by posting on the first *business* day.

### 3.7 `internal/ledger/consumer` — four delivery modes (FR-L8)

```go
type Mode string
const ( AtMostOnce Mode = "A"; AtLeastOnce Mode = "B"; InboxDedup Mode = "C"; Transactional Mode = "D" )

type Consumer interface{ Run(ctx context.Context) error }   // returns on ctx.Done() after draining
```

| Mode | Offset commit | DB work | Expected under broker kill |
|---|---|---|---|
| A | before apply | plain insert | **lost** > 0, duplicated = 0 |
| B | after apply | plain insert | lost = 0, **duplicated** > 0 |
| C | after apply | `INSERT inbox(message_id)` + apply in one txn | lost = 0, duplicated = 0, higher p99 |
| D | `kgo.GroupTransactSession` | apply in DB txn, offsets committed transactionally | lost = 0, duplicated = 0; guarantee ends at the DB boundary |

Mode C's duplicate detection is again a `23505` on `inbox_pkey` — same mechanism as idempotency, same reason.

### 3.8 `internal/ledger/outbox` — relay (FR-L7)

Polls `outbox_unsent` in `outbox_id` order, batches up to 500, produces with `acks=all` keyed by `partition_key`, marks `sent_at` only after every promise in the batch resolves. At-least-once by construction; ordering per account is preserved because the key is the account and the poll is ordered. Runs with `ctx.Done()` in its select; shutdown drains the in-flight batch before returning (FR-L10).

### 3.9 `legacy-sim` (Python)

```
calendar.py   business-day calendar incl. holidays and leap years (FR-S5);
              mirrors internal/bizdate, golden-tested against it for 2027-2029
generator.py  seeded stream (FR-S1): accounts, products (CHK-01, SAV-01),
              currencies (USD, JPY)
quirks.py     one pure function per quirk, all twelve switchable (FR-S2)
extracts.py   header/detail/trailer writer (FR-S3)
emit.py       HTTP ingress (Finding 1) and topic ingress (Finding 2)  (FR-S4, D-012)
```

Every quirk is a pure function over the record stream, registered by `id`, applied only when `quirks.yaml` enables it. That is what makes "quirk off" a genuine control and lets a test run each quirk in isolation.

**Extract format** — pipe-delimited, one file per extract type per business day:

```
HDR|SHADOWBOOK|TXN|20280229|001|<seed>
DTL|<txn_id>|<account_id>|<currency>|<amount_minor>|<scale>|<posted_at>|<value_date>|<kind>
…
TRL|<record_count>|USD:-1250000;JPY:0
```

The trailer's control total is a **per-currency signed sum** of `amount_minor` over the detail records — multi-currency is required by FR-S1, so a single scalar total would be meaningless. `BAL` extracts carry the same envelope with `DTL|<account_id>|<currency>|<ledger_minor>|<available_minor>` rows.

**Determinism (FR-S6):** one `random.Random(seed_for("legacy-sim"))` instance; all dict iteration replaced by `sorted()`; timestamps derived from the simulated calendar, never `datetime.now()`; files written with `\n` and a trailing newline, UTF-8, no locale dependence. A golden test hashes every extract for W1 and W2.

### 3.10 `reconcile` (Python)

```
ingest.py     parse + validate trailer; tolerant of late, redelivered and
              truncated extracts (FR-R4); idempotent on
              (extract_type, business_date, sequence)
grains.py     the three comparators: transaction, account-day, control total (FR-R1)
classify.py   deterministic rules -> timing | model_difference | defect
age.py        open/close/age breaks across business days
discovery.py  per-quirk time-to-discovery (FR-R3)
store.py      psycopg to the recon DB
```

```sql
CREATE TABLE extract_ingests (
    extract_type  TEXT NOT NULL, business_date DATE NOT NULL, sequence INT NOT NULL,
    file_sha256   BYTEA NOT NULL, record_count INT NOT NULL, control_total JSONB NOT NULL,
    status        TEXT NOT NULL
        CHECK (status IN ('accepted','trailer_mismatch','truncated','duplicate','late')),
    received_on   DATE NOT NULL,
    PRIMARY KEY (extract_type, business_date, sequence)
);

CREATE TABLE breaks (
    break_id       BIGSERIAL PRIMARY KEY,
    grain          TEXT NOT NULL CHECK (grain IN ('transaction','account_day','control_total')),
    business_date  DATE NOT NULL,
    account_id     UUID NULL, txn_id TEXT NULL, currency CHAR(3) NOT NULL,
    legacy_minor   BIGINT NULL, shadow_minor BIGINT NULL, delta_minor BIGINT NOT NULL,
    classification TEXT NOT NULL
        CHECK (classification IN ('timing','model_difference','defect')),
    signature      TEXT NOT NULL,          -- deterministic; drives quirk attribution
    attributed_quirk TEXT NULL,
    first_seen_on  DATE NOT NULL, last_seen_on DATE NOT NULL, closed_on DATE NULL,
    age_business_days INT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX breaks_identity
    ON breaks (grain, business_date, coalesce(account_id::text,''), coalesce(txn_id,''), currency);

CREATE TABLE quirk_discovery (
    quirk_id TEXT NOT NULL, window_id TEXT NOT NULL,
    detected BOOLEAN NOT NULL,
    first_detected_business_day INT NULL, first_detected_grain TEXT NULL,
    breaks_at_first_detection INT NULL, breaks_to_isolate INT NULL,
    PRIMARY KEY (quirk_id, window_id)
);
```

**Classification is deterministic and rule-ordered (FR-R2, FR-R5 — no LLM):**

1. **timing** — the same `(account_id, amount_minor, currency)` appears on the opposite side within ±2 business days and is otherwise unmatched.
2. **model_difference** — both sides have a record for the same key but the amounts differ, and the delta is reproduced by at least one rule in the model-rule table below.
3. **defect** — everything else: one-sided beyond the ageing window, or a delta no rule explains.

| Model rule | Test on the delta | Signature |
|---|---|---|
| rounding | `abs(delta) <= 1` minor unit | `round:1` |
| day-count 365/360 | `shadow * 365 == legacy * 360` (integer cross-multiply) | `basis:365_360` |
| day-count leap | delta equals one day's accrual at ACT/365 on a leap year | `basis:leap` |
| scale | `abs(legacy) == abs(shadow) * 100` and currency scale is 0 | `scale:100` |
| whole-fee | delta equals a configured fee amount exactly | `fee:<code>` |
| whole-txn | delta equals an existing transaction amount exactly | `txn:whole` |

**Quirk attribution is separate from classification** and never feeds back into it: a deterministic map from `(grain, signature, cadence context)` to a candidate quirk set, used only by `discovery.py`. Keeping these apart is what stops the reconciler from "knowing the answer" — classification cannot cheat by looking up which quirk is enabled.

- `breaks_at_first_detection` = count of open breaks on the business day the quirk's signature first appears.
- `breaks_to_isolate` = breaks accumulated with that signature from first detection until the candidate set narrows to exactly one quirk. If it never narrows to one, the quirk is reported `detected=true, breaks_to_isolate=null` — surfaced, not isolated. Both are distinct from `detected=false`, and `FINDINGS.md` shows all three states.

### 3.11 `internal/harness`

```go
// load — FR-H1. Both are seeded and unit-testable (HLD §7.7).
func PacerFor(profile Profile) vegeta.Pacer
func TargeterFor(profile Profile, seed int64, accounts []uuid.UUID) vegeta.Targeter

// chaos — FR-H2
type Event struct{ At time.Duration; Action string; Broker string } // kill | start
func Run(ctx context.Context, sched []Event, d Docker) error

// ablation — FR-H3
type RunArtefact struct {
    Config        string    // A | B | C | D
    Seed          int64
    Profile       string
    RatePerSec    int       // must be equal across artefacts (D-007)
    Schedule      []Event
    LedgerSHA     string
    BrokerVersion string
    Sent, Applied, Lost, Duplicated int64
    P50, P95, P99 time.Duration
    LagPeak       int64
    DrainSeconds  float64
    InvariantHeld bool
}
```

`Lost = Sent − Applied − (still in flight at drain end)`; `Duplicated` = count of `message_id` values applied more than once, measured by querying the ledger, not inferred from the broker. The runner refuses to render a table when `Seed`, `Profile`, `RatePerSec`, `Schedule`, `LedgerSHA` or `BrokerVersion` differ across artefacts (`chaos-ablation` skill).

## 4. Shared contracts — FROZEN on approval

### 4.1 Protobuf (`contracts/shadowbook/v1/`)

```protobuf
syntax = "proto3";
package shadowbook.v1;

message Money {                 // mirrors internal/money.Amount exactly
  int64  minor    = 1;
  string currency = 2;          // ISO 4217
  uint32 scale    = 3;
}

message Entry {
  int64  entry_id   = 1;
  string account_id = 2;
  Money  amount     = 3;
}

// Emitted by the ledger's outbox, one per committed posting (FR-L7).
message PostingEvent {
  string posting_id    = 1;
  string principal     = 2;
  string kind          = 3;
  string business_date = 4;     // YYYY-MM-DD
  string value_date    = 5;
  string posted_at     = 6;     // RFC3339 UTC
  repeated Entry entries = 7;
  string reverses_posting_id = 8;
}

// Consumed by the ledger from legacy-sim on the ablation path (FR-L8, D-012).
message MovementEvent {
  string message_id    = 1;     // idempotency identity; the inbox PRIMARY KEY
  string account_id    = 2;
  Money  amount        = 3;
  string business_date = 4;
  string value_date    = 5;
  string posted_at     = 6;
  string kind          = 7;
}
```

`buf breaking` runs against `main` in CI. Generated code for both languages is checked into `gen/` and CI fails if regenerating produces a diff — that, not discipline, is what keeps the two languages in step (D-013).

### 4.2 Topics

| Topic | Key | Partitions | Producer | Consumer |
|---|---|---|---|---|
| `shadowbook.postings.v1` | `account_id` | 6 | ledger outbox relay | (observability only) |
| `shadowbook.movements.v1` | `account_id` | 6 | legacy-sim | ledger consumer |

Fixed for every run (NFR-9): `acks=all`, RF=3, `min.insync.replicas=2`, unclean leader election off. Keying by `account_id` is what makes per-account ordering hold, which the hot-key scenario deliberately stresses.

### 4.3 Extract file contract

Defined in §3.9 and mirrored in `contracts/shadowbook/v1/extract.proto` as documentation only — the wire format is the text file, and a golden test pins it byte-for-byte.

### 4.4 Run artefact

`reports/runs/<run_id>/artefact.json`, schema = `harness/ablation.RunArtefact` (§3.11). `make report` (FR-H4) reads only these; it never touches a live system. `make demo` (FR-H5) runs both windows of D-010 end to end and prints both findings inside NFR-4's five minutes.

## 5. Data migration and versioning policy

- **Forward-only**, numbered `NNNN_slug.sql`, applied by goose at ledger start-up from `embed.FS`. No down migrations: this is a harness whose databases are recreated per run, and a reversible migration on an append-only table is a fiction.
- **Recon DB is disposable** — dropped and recreated by `make demo`. Its migrations live under `reconcile/migrations/`.
- **API versioning** — `/v1` path prefix. There will be no `/v2`; the prefix exists so the contract is explicit rather than accidental.
- **Seed data** — accounts and products are generated by `legacy-sim` from the seed, never inserted by a migration. A migration that carried fixture data would break FR-S6 and NFR-5.

## 6. Test strategy

| Layer | What it covers | Tooling |
|---|---|---|
| Unit — Go | `money` arithmetic and scale rejection; `bizdate` boundaries; classification-adjacent pure logic; pacers and targeters (seeded, so assertions are exact) | table-driven `testing`, `-race` |
| Unit — Python | each quirk function in isolation; extract writer; classification rules; calendar mirror | pytest, parametrised |
| Integration | posting path against real Postgres; DDL invariants (append-only raises, zero-sum rejects, 23505 path); outbox relay; each consumer mode against real Redpanda | testcontainers-go, one shared container per package |
| Contract | HTTP ingress vs topic ingress produce identical entries, balances, outbox rows (D-012, risk R6); `buf breaking`; `gen/` diff | integration suite + CI |
| Golden | extracts byte-identical for W1 and W2; Finding 1 table identical for a fixed seed | committed golden files |
| Scenario | every named adversarial scenario in the README maps to a test **by name** | integration |
| Performance smoke | steady profile at NFR-1 for 30 s, assert p99 ≤ 50 ms; ablation runs asserted equal-rate at NFR-1a | harness, Phase 6 only |

`make check` must stay under three minutes (NFR-6) and pass with the network disabled (NFR-8): generated protobuf code is checked in, migrations are embedded, and container images are pinned by digest so nothing is fetched at check time. Golden tests are the determinism gate (NFR-5).

**Coverage (NFR-7):** ledger ≥ 85% with posting path and invariants ≥ 95%; reconcile classification ≥ 90%; legacy-sim ≥ 85% (D-011). Enforced in `make check`, not aspirational.

**Named scenario → test map** (README promises this):

| Scenario | Test |
|---|---|
| Q1 … Q12 | `TestQuirkDetected/<id>` per quirk, per window |
| ablation A/B/C/D | `TestDeliveryMode/<mode>` |
| hot-key | `TestHotKeyOrdering` |
| late extract | `TestIngestLateExtract` |
| redelivered extract | `TestIngestRedelivered` |
| truncated extract, bad trailer | `TestIngestTruncatedTrailerMismatch` |
| out-of-order event | `TestConsumerOutOfOrder` |
| idempotency race | `TestIdempotencyRace64` |
| broker kill in flight | `TestBrokerKillDuringBatch` |

## 7. Observability plan

| Metric | Type | Purpose |
|---|---|---|
| `shadowbook_postings_total{kind,result}` | counter | throughput, NFR-1 |
| `shadowbook_posting_duration_seconds` | histogram | p99, NFR-2 |
| `shadowbook_ledger_invariant_ok` | gauge 0/1 | FR-L9 |
| `shadowbook_ledger_invariant_last_check_seconds` | gauge | NFR-3 lag |
| `shadowbook_outbox_depth` | gauge | relay health |
| `shadowbook_consumer_lag{mode}` | gauge | Finding 2 |
| `shadowbook_movements_total{mode,result}` | counter | applied / duplicate-suppressed |
| `shadowbook_eod_duration_seconds{phase}` | histogram | accrual cost |

**Logged:** request ID, principal, business date, posting ID, error code, mode.
**Never logged:** full request bodies, idempotency keys, database passwords, `.env` contents. Amounts are logged only in aggregate — a portfolio repo is public, and the habit is the point.

Health: `/healthz` is liveness (process up); `/readyz` is readiness (migrations applied, DB reachable, broker reachable). Traces are out of scope — a four-process pipeline with structured logs and a shared request ID does not need OpenTelemetry, and adding it would cost `make check` time for no finding.

## 8. Verification performed on this document

The DDL and the balance query in this document were executed verbatim against **PostgreSQL 16.13**, not merely written. Extracted from the markdown, applied to an empty database, and exercised:

| Check | Result |
|---|---|
| All eleven tables, indexes, triggers and functions apply clean from an empty database | pass |
| Balanced posting commits | pass |
| Unbalanced posting (sums to 899) rejected at COMMIT by the deferred trigger | pass — `posting … sums to 899, need 0` |
| Single-entry posting rejected | pass — `has 1 entries, need >= 2` |
| `UPDATE` on `entries` raises | pass — `entries is append-only` |
| `DELETE` on `entries` raises | pass |
| Duplicate `(principal, idem_key)` raises 23505 on `idempotency_keys_pkey` | pass |
| Duplicate `message_id` raises 23505 on `inbox_pkey` | pass |
| Global invariant `SUM(amount_minor) GROUP BY currency` = 0 after the rejections | pass |
| Balance query: checkpoint + entries / entries only / neither | pass (375000 / -125000 / 0) |

Two defects were found this way and are already fixed above: `entries.entry_id` was typed `BIGGSERIAL`, and the balance query was missing its mandatory `GROUP BY` and returned no row at all for an account with no checkpoint yet. Both would have cost a Phase 4 session.

## 9. Open questions for this gate

None. Every decision above is either settled by an ADR or is a judgement call recorded here to be overridden if you disagree — the three most likely candidates for disagreement, called out honestly:

1. **Forward-only migrations with no down path** (§5). Defensible for a disposable harness, unusual elsewhere.
2. **`reconcile` writes to its own Postgres rather than emitting files** (§3.10). It buys break ageing across days cheaply; it costs a second database in compose.
3. **No OpenTelemetry** (§7). A deliberate omission, not an oversight.
