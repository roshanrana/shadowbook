-- 0005 derived balances and holds. No column anywhere is "the balance":
-- balance(account, t) = checkpoint + SUM(entries after it) (FR-L4).
CREATE TABLE checkpoints (
    account_id    UUID        NOT NULL REFERENCES accounts(account_id),
    business_date DATE        NOT NULL,
    currency      CHAR(3)     NOT NULL,
    balance_minor BIGINT      NOT NULL,
    last_entry_id BIGINT      NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, business_date)
);

-- Checkpoints are inserts, never updates (LLD §5.2).
CREATE TRIGGER checkpoints_append_only BEFORE UPDATE OR DELETE ON checkpoints
    FOR EACH STATEMENT EXECUTE FUNCTION deny_mutation();

-- Holds reduce AVAILABLE balance, never ledger balance, and expire 72 hours
-- after placement (FR-L5). Q8 is the legacy core expiring them at midnight on
-- placement + 3, so expires_at is the field Q8 is measured against.
CREATE TABLE holds (
    hold_id      UUID PRIMARY KEY,
    account_id   UUID        NOT NULL REFERENCES accounts(account_id),
    currency     CHAR(3)     NOT NULL,
    amount_minor BIGINT      NOT NULL CHECK (amount_minor > 0),
    placed_at    TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    released_at  TIMESTAMPTZ NULL,
    release_kind TEXT        NULL CHECK (release_kind IN ('captured','cancelled','expired'))
);

CREATE INDEX holds_open ON holds (account_id) WHERE released_at IS NULL;
