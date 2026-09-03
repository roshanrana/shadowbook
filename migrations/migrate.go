// Package migrations holds the ledger schema as plain SQL and applies it.
//
// The files are embedded so `make check` never reaches the filesystem or the
// network for them (NFR-8), and migrations are forward-only: there are no down
// files, by decision (D-014).
//
// LLD §5 and D-013 named goose as the applier. It is replaced here by ~60 lines
// of database/sql -- see decisions.md D-018 for why and for how to revert. The
// substance the LLD actually specified is unchanged: plain SQL, numbered,
// forward-only, embedded, applied in order at start-up.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var FS embed.FS

// Migration is one numbered SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Load reads and orders the embedded migrations.
func Load() ([]Migration, error) {
	entries, err := fs.Glob(FS, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("migrations: glob: %w", err)
	}
	out := make([]Migration, 0, len(entries))
	for _, name := range entries {
		verStr, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migrations: %q is not NNNN_slug.sql", name)
		}
		v, err := strconv.Atoi(verStr)
		if err != nil {
			return nil, fmt.Errorf("migrations: %q has a non-numeric version: %w", name, err)
		}
		body, err := FS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("migrations: read %q: %w", name, err)
		}
		out = append(out, Migration{Version: v, Name: name, SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })

	for i := range out {
		if want := i + 1; out[i].Version != want {
			return nil, fmt.Errorf("migrations: version %d is missing (found %d at position %d)",
				want, out[i].Version, i)
		}
	}
	return out, nil
}

const versionTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INT PRIMARY KEY,
    name        TEXT        NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Apply runs every unapplied migration in order, each in its own transaction,
// and records it. Re-running is a no-op.
func Apply(ctx context.Context, db *sql.DB) (applied int, err error) {
	if _, err := db.ExecContext(ctx, versionTable); err != nil {
		return 0, fmt.Errorf("migrations: version table: %w", err)
	}

	done := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return 0, fmt.Errorf("migrations: read applied: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return 0, fmt.Errorf("migrations: scan applied: %w", err)
		}
		done[v] = true
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("migrations: iterate applied: %w", err)
	}

	all, err := Load()
	if err != nil {
		return 0, err
	}
	for _, m := range all {
		if done[m.Version] {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

func applyOne(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrations: begin %s: %w", m.Name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("migrations: apply %s: %w", m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.Version, m.Name); err != nil {
		return fmt.Errorf("migrations: record %s: %w", m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrations: commit %s: %w", m.Name, err)
	}
	return nil
}
