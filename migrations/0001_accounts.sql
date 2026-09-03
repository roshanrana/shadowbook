-- 0001 accounts. Forward-only; there is no down migration anywhere in this
-- tree (D-014): the databases are recreated per run, and a reversible
-- migration on an append-only table is a fiction.
CREATE TABLE accounts (
    account_id    UUID PRIMARY KEY,
    product_code  TEXT        NOT NULL,
    currency      CHAR(3)     NOT NULL,
    opened_on     DATE        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX accounts_product ON accounts (product_code);
