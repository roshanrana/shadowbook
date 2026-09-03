-- 0003 the three invariants that live in DDL rather than in application code.
-- Verified against PostgreSQL 16.13 before this schema was approved (D-015,
-- LLD §8): each assertion below is known to fire.

-- 1. Append-only (FR-L3). Statement-level, so a bulk UPDATE cannot slip past a
--    row-level guard. Reversals insert contra entries carrying
--    reverses_entry_id; they never delete. Q9 is the legacy core deleting the
--    original, which the shadow is structurally unable to do.
CREATE FUNCTION deny_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'SHADOWBOOK: % is append-only', TG_TABLE_NAME; END $$;

CREATE TRIGGER entries_append_only     BEFORE UPDATE OR DELETE ON entries
    FOR EACH STATEMENT EXECUTE FUNCTION deny_mutation();

-- 2. Zero-sum per posting (FR-L2). DEFERRED so a multi-entry insert is legal
--    mid-transaction and is judged at COMMIT.
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

-- 3. Idempotency is the PRIMARY KEY on (principal, idem_key) in 0004. There is
--    no other mechanism, and no SELECT-then-INSERT anywhere in the posting path.
