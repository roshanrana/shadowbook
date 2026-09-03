-- 0002 the double-entry core. Every posting has >= 2 entries summing to zero
-- (FR-L2); entries are immutable (FR-L3). Both are enforced in 0003, in the
-- database, not in Go.
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

CREATE INDEX postings_business_date ON postings (business_date);

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
