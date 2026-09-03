-- 0004 idempotency as a database constraint, not application logic (CLAUDE.md).
-- A duplicate is detected by the 23505 violation, never by a prior SELECT --
-- which is what makes the N-concurrent-same-key scenario deterministic.
CREATE TABLE idempotency_keys (
    principal  TEXT        NOT NULL,
    idem_key   TEXT        NOT NULL,
    body_hash  BYTEA       NOT NULL,
    posting_id UUID        NULL REFERENCES postings(posting_id),
    response   JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (principal, idem_key)
);
