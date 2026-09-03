// Package testsupport gives every integration test its own database.
//
// The obvious approach -- point every test at one database and
// `DROP SCHEMA public CASCADE` in the fixture -- fails as soon as `go test`
// runs packages in parallel, which it does by default: two packages reset the
// schema underneath each other and the failures look like migration bugs.
// Serialising with `-p 1` would hide it rather than fix it, and would still
// break for two developers sharing a database.
//
// So each test creates a database named after itself, migrates it, and drops it
// on cleanup. Tests are then independent by construction.
package testsupport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

// DSN returns the ledger DSN, or skips the test when there is no database.
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SHADOWBOOK_LEDGER_DSN")
	if dsn == "" {
		t.Skip("SHADOWBOOK_LEDGER_DSN unset; run `make up` first")
	}
	return dsn
}

// dbName derives a legal, unique, stable database name from the test name.
// Postgres identifiers cap at 63 bytes, so long test names are hashed.
func dbName(testName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '_'
		}
	}, testName)
	sum := sha256.Sum256([]byte(testName))
	suffix := hex.EncodeToString(sum[:4])
	if len(safe) > 40 {
		safe = safe[:40]
	}
	return fmt.Sprintf("sb_%s_%s", safe, suffix)
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("testsupport: parse dsn: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// FreshStore creates a database for this test, applies every migration, and
// drops it when the test finishes.
func FreshStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	adminDSN := DSN(t)

	admin, err := store.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("testsupport: connect: %v", err)
	}
	defer admin.Close()

	name := dbName(t.Name())
	// A leftover from a killed run would otherwise fail the create.
	if _, err := admin.Pool.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`"`); err != nil {
		t.Fatalf("testsupport: drop stale %s: %v", name, err)
	}
	if _, err := admin.Pool.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("testsupport: create %s: %v", name, err)
	}

	testDSN, err := withDatabase(adminDSN, name)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("testsupport: open %s: %v", name, err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("testsupport: migrate %s: %v", name, err)
	}

	t.Cleanup(func() {
		st.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		a, err := store.Open(cleanupCtx, adminDSN)
		if err != nil {
			return
		}
		defer a.Close()
		_, _ = a.Pool.Exec(cleanupCtx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
	})
	return st
}

// SeedAccounts inserts n CHK-01 accounts with stable ids, and returns them.
func SeedAccounts(t *testing.T, st *store.Store, n int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	out := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.NewSHA1(uuid.MustParse("6f1d2c3b-4a59-5e87-9b02-1c8d7e4f6a35"),
			[]byte(fmt.Sprintf("%s/%d", t.Name(), i)))
		if err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: id, ProductCode: "CHK-01", Currency: "USD",
			OpenedOn: bizdate.Date(2018, time.June, 1),
		}); err != nil {
			t.Fatalf("testsupport: seed account %d: %v", i, err)
		}
		out = append(out, id)
	}
	return out
}
