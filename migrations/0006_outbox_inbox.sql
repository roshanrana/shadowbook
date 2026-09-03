-- 0006 the transactional outbox (FR-L7) and the consumer inbox (FR-L8 mode C).
-- Both are written in the same transaction as their effect; neither is a queue
-- the application polls for correctness.
CREATE TABLE outbox (
    outbox_id     BIGSERIAL PRIMARY KEY,
    posting_id    UUID        NOT NULL REFERENCES postings(posting_id),
    partition_key TEXT        NOT NULL,
    payload       BYTEA       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at       TIMESTAMPTZ NULL
);

CREATE INDEX outbox_unsent ON outbox (outbox_id) WHERE sent_at IS NULL;

-- message_id is an identity, not a hint. Mode C's exactly-once effect IS the
-- 23505 on this primary key -- the same mechanism as idempotency_keys.
CREATE TABLE inbox (
    message_id  TEXT PRIMARY KEY,
    topic       TEXT   NOT NULL,
    partition   INT    NOT NULL,
    msg_offset  BIGINT NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- EOD is triggered by the harness, never by wall-clock time, and is idempotent
-- per business date: a replay returns EODAlreadyRun (LLD §3.6).
CREATE TABLE eod_runs (
    business_date DATE PRIMARY KEY,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ NULL,
    phases        JSONB       NOT NULL DEFAULT '{}'::jsonb
);
