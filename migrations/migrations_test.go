//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/roshanrana/shadowbook/internal/testsupport"
	"github.com/roshanrana/shadowbook/migrations"
)

// These assertions are not speculative. Every one of them was executed against
// PostgreSQL 16.13 before the schema was approved (D-015, LLD §8), so a failure
// here means the SQL was transcribed wrongly, not that the design is wrong.

// freshDB gives each test its OWN database, not a reset schema in a shared one.
//
// go test runs packages in parallel by default. Resetting one shared schema
// meant two packages tore each other's tables down mid-run, and the failures
// looked like migration bugs rather than a fixture problem.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	st := testsupport.FreshStore(t) // creates, migrates and drops its own database
	db, err := sql.Open("pgx", st.DSN())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	all, err := migrations.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("loaded %d migrations, want 6", len(all))
	}
	return db
}

func TestApplyIsCleanAndIdempotent(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()

	// Re-running must be a no-op.
	n, err := migrations.Apply(ctx, db)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if n != 0 {
		t.Fatalf("re-apply applied %d migrations, want 0", n)
	}

	want := []string{
		"accounts", "checkpoints", "entries", "eod_runs", "holds",
		"idempotency_keys", "inbox", "outbox", "postings", "schema_migrations",
	}
	rows, err := db.QueryContext(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY 1`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tables = %v\nwant %v", got, want)
	}
}

func TestNoDownMigrationsExist(t *testing.T) {
	all, err := migrations.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("loaded %d migrations, want 6", len(all))
	}
	for _, m := range all {
		if strings.Contains(strings.ToLower(m.Name), "down") {
			t.Fatalf("%s looks like a down migration; D-014 says forward-only", m.Name)
		}
		if strings.Contains(m.SQL, "BIGGSERIAL") {
			t.Fatalf("%s contains BIGGSERIAL -- the defect D-015 already fixed", m.Name)
		}
	}
}

func seedAccounts(t *testing.T, db *sql.DB) (a, b string) {
	t.Helper()
	a, b = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	for _, id := range []string{a, b} {
		if _, err := db.Exec(
			`INSERT INTO accounts (account_id, product_code, currency, opened_on)
			 VALUES ($1,'CHK-01','USD','2018-06-01')`, id); err != nil {
			t.Fatalf("seed account: %v", err)
		}
	}
	return a, b
}

func postingWithEntries(t *testing.T, db *sql.DB, postingID string, amounts ...int64) error {
	t.Helper()
	accA, accB := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		`INSERT INTO postings (posting_id, principal, kind, currency, business_date, value_date, posted_at)
		 VALUES ($1,'sim','transfer','USD','2028-02-29','2028-02-29', now())`, postingID); err != nil {
		return err
	}
	for i, amt := range amounts {
		acc := accA
		if i%2 == 1 {
			acc = accB
		}
		if _, err := tx.Exec(
			`INSERT INTO entries (posting_id, account_id, currency, amount_minor, scale, business_date, value_date)
			 VALUES ($1,$2,'USD',$3,2,'2028-02-29','2028-02-29')`, postingID, acc, amt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FR-L2: >= 2 entries summing to zero, judged at COMMIT by the deferred trigger.
func TestZeroSumEnforcedAtCommit(t *testing.T) {
	db := freshDB(t)
	seedAccounts(t, db)

	if err := postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000001", -125000, 125000); err != nil {
		t.Fatalf("balanced posting must commit: %v", err)
	}

	err := postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000002", -100, 999)
	if err == nil {
		t.Fatal("unbalanced posting committed; the zero-sum trigger did not fire")
	}
	if !strings.Contains(err.Error(), "sums to 899, need 0") {
		t.Fatalf("unbalanced posting error = %v, want the zero-sum message", err)
	}

	err = postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000003", 0)
	if err == nil {
		t.Fatal("single-entry posting committed; the >= 2 check did not fire")
	}
	if !strings.Contains(err.Error(), "has 1 entries, need >= 2") {
		t.Fatalf("single-entry error = %v, want the entry-count message", err)
	}
}

// FR-L3: entries and checkpoints are append-only, enforced statement-level so a
// bulk UPDATE cannot slip past.
func TestAppendOnly(t *testing.T) {
	db := freshDB(t)
	seedAccounts(t, db)
	if err := postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000001", -1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO checkpoints (account_id, business_date, currency, balance_minor, last_entry_id)
		VALUES ('11111111-1111-1111-1111-111111111111','2028-02-28','USD',0,0)`); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, stmt string }{
		{"update entries", `UPDATE entries SET amount_minor = 1`},
		{"delete entries", `DELETE FROM entries`},
		{"update entries by id", `UPDATE entries SET amount_minor = 5 WHERE entry_id = 1`},
		{"update checkpoints", `UPDATE checkpoints SET balance_minor = 99`},
		{"delete checkpoints", `DELETE FROM checkpoints`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(tc.stmt)
			if err == nil {
				t.Fatalf("%q succeeded; the table is not append-only", tc.stmt)
			}
			if !strings.Contains(err.Error(), "is append-only") {
				t.Fatalf("error = %v, want the append-only message", err)
			}
		})
	}
}

// Idempotency and inbox dedup are the SAME mechanism: a 23505 on a primary key.
// The typed *pgconn.PgError is what D-013 chose pgx for.
func TestDuplicatesRaise23505WithNamedConstraint(t *testing.T) {
	db := freshDB(t)

	for _, tc := range []struct {
		name, first, second, constraint string
	}{
		{
			name:       "idempotency key",
			first:      `INSERT INTO idempotency_keys (principal, idem_key, body_hash, response) VALUES ('sim','k1','\x00','{}')`,
			second:     `INSERT INTO idempotency_keys (principal, idem_key, body_hash, response) VALUES ('sim','k1','\xFF','{}')`,
			constraint: "idempotency_keys_pkey",
		},
		{
			name:       "inbox message id",
			first:      `INSERT INTO inbox (message_id, topic, partition, msg_offset) VALUES ('m-1','shadowbook.movements.v1',0,10)`,
			second:     `INSERT INTO inbox (message_id, topic, partition, msg_offset) VALUES ('m-1','shadowbook.movements.v1',0,11)`,
			constraint: "inbox_pkey",
		},
		{
			name:       "eod replay",
			first:      `INSERT INTO eod_runs (business_date) VALUES ('2028-02-29')`,
			second:     `INSERT INTO eod_runs (business_date) VALUES ('2028-02-29')`,
			constraint: "eod_runs_pkey",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(tc.first); err != nil {
				t.Fatalf("first insert: %v", err)
			}
			_, err := db.Exec(tc.second)
			if err == nil {
				t.Fatal("duplicate accepted; the constraint is missing")
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("error is not a *pgconn.PgError: %T %v", err, err)
			}
			if pgErr.Code != "23505" {
				t.Fatalf("SQLSTATE = %s, want 23505", pgErr.Code)
			}
			if pgErr.ConstraintName != tc.constraint {
				t.Fatalf("constraint = %q, want %q", pgErr.ConstraintName, tc.constraint)
			}
		})
	}
}

// The global invariant must hold after the rejections above, not only on a
// clean database (FR-L9).
func TestGlobalInvariantHoldsAfterRejections(t *testing.T) {
	db := freshDB(t)
	seedAccounts(t, db)
	_ = postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000001", -125000, 125000)
	_ = postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000002", -100, 999) // rejected
	_ = postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000003", 7)         // rejected

	rows, err := db.Query(`SELECT currency, sum(amount_minor) FROM entries GROUP BY currency`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var cur string
		var drift int64
		if err := rows.Scan(&cur, &drift); err != nil {
			t.Fatal(err)
		}
		if drift != 0 {
			t.Fatalf("global invariant broken: %s drift = %d", cur, drift)
		}
		seen++
	}
	if seen != 1 {
		t.Fatalf("expected exactly one currency group, got %d", seen)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM entries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("entry rows = %d, want 2 (only the balanced posting survived)", n)
	}
}

// The balance query from LLD §3.3, in all three shapes. The no-checkpoint case
// is the defect D-015 caught: it must return one row valued 0, not no rows.
func TestDerivedBalanceQuery(t *testing.T) {
	db := freshDB(t)
	accA, _ := seedAccounts(t, db)
	if _, err := db.Exec(`INSERT INTO accounts (account_id, product_code, currency, opened_on)
		VALUES ('44444444-4444-4444-4444-444444444444','CHK-01','USD','2022-01-01')`); err != nil {
		t.Fatal(err)
	}
	if err := postingWithEntries(t, db, "aaaaaaaa-0000-0000-0000-000000000001", -125000, 125000); err != nil {
		t.Fatal(err)
	}

	const q = `
SELECT coalesce(c.balance_minor, 0) + coalesce(sum(e.amount_minor), 0) AS ledger_minor
FROM       (SELECT 1) AS anchor
LEFT JOIN LATERAL (SELECT balance_minor, last_entry_id FROM checkpoints
                   WHERE account_id = $1 AND business_date <= $2
                   ORDER BY business_date DESC LIMIT 1) c ON true
LEFT JOIN  entries e ON e.account_id = $1
                    AND e.entry_id > coalesce(c.last_entry_id, 0)
                    AND e.business_date <= $2
GROUP BY c.balance_minor`

	var got int64
	if err := db.QueryRow(q, accA, "2028-02-29").Scan(&got); err != nil {
		t.Fatalf("entries, no checkpoint: %v", err)
	}
	if got != -125000 {
		t.Fatalf("entries with no checkpoint = %d, want -125000", got)
	}

	if _, err := db.Exec(`INSERT INTO checkpoints (account_id, business_date, currency, balance_minor, last_entry_id)
		VALUES ($1,'2028-02-28','USD',500000,0)`, accA); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(q, accA, "2028-02-29").Scan(&got); err != nil {
		t.Fatalf("checkpoint + entries: %v", err)
	}
	if got != 375000 {
		t.Fatalf("checkpoint + entries = %d, want 375000", got)
	}

	if err := db.QueryRow(q, "44444444-4444-4444-4444-444444444444", "2028-02-29").Scan(&got); err != nil {
		t.Fatalf("no checkpoint and no entries must still return one row: %v", err)
	}
	if got != 0 {
		t.Fatalf("empty account = %d, want 0", got)
	}
}
