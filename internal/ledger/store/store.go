// Package store is the ledger's only door to PostgreSQL.
//
// One rule shapes the whole package: database errors are returned UNWRAPPED.
// The posting path decides what a unique-constraint violation means by
// inspecting SQLSTATE and the constraint name (LLD §3.5, execution plan §4), so
// a store that translated errors into its own taxonomy would quietly destroy
// idempotency-by-constraint. Callers use errors.As with *pgconn.PgError.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver, used only for migrations

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/money"
	"github.com/roshanrana/shadowbook/migrations"
)

// SQLSTATE codes the posting path branches on.
const (
	SQLStateUniqueViolation = "23505"
	SQLStateRaiseException  = "P0001" // our DDL invariants RAISE with this code
)

// Constraint names, so callers never string-match on message text.
const (
	ConstraintIdempotency = "idempotency_keys_pkey"
	ConstraintInbox       = "inbox_pkey"
	ConstraintEODRun      = "eod_runs_pkey"
)

// Queryer is satisfied by both *pgxpool.Pool and pgx.Tx, so every query below
// works inside or outside a caller-controlled transaction.
type Queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store owns the connection pool.
type Store struct {
	Pool *pgxpool.Pool
	dsn  string
}

// Open connects and verifies the connection.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("store: parse dsn: %w", err)
	}
	// Sized for NFR-1 (>= 2000 postings/s on one box) with one transaction per
	// posting; tune here, not at the call sites.
	cfg.MaxConns = 32
	cfg.MinConns = 4
	cfg.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{Pool: pool, dsn: dsn}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.Pool.Close() }

// Ping is the readiness probe's database half.
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

// Migrate applies the embedded migrations. Forward-only (D-014, D-018).
func (s *Store) Migrate(ctx context.Context) (int, error) {
	db, err := sql.Open("pgx", s.dsn)
	if err != nil {
		return 0, fmt.Errorf("store: migrate open: %w", err)
	}
	defer func() { _ = db.Close() }()
	return migrations.Apply(ctx, db)
}

// Begin starts a transaction with constraints deferred, which is what lets a
// multi-entry posting be inserted row by row and judged as a whole at COMMIT.
func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("store: defer constraints: %w", err)
	}
	return tx, nil
}

// IsUniqueViolation reports whether err is a 23505 on the named constraint.
// Passing an empty constraint matches any.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != SQLStateUniqueViolation {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// IsInvariantViolation reports whether err came from one of the DDL invariant
// triggers (zero-sum, entry count, append-only) rather than from a constraint.
func IsInvariantViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == SQLStateRaiseException
}

// ---------------------------------------------------------------- model types

// Posting is the transaction envelope.
type Posting struct {
	ID                uuid.UUID
	Principal         string
	Kind              string
	Currency          money.Currency
	BusinessDate      bizdate.BusinessDate
	ValueDate         bizdate.BusinessDate
	PostedAt          time.Time
	ReversesPostingID *uuid.UUID
}

// Entry is one leg. Amount carries its own currency and scale.
type Entry struct {
	ID              int64
	PostingID       uuid.UUID
	AccountID       uuid.UUID
	Amount          money.Amount
	BusinessDate    bizdate.BusinessDate
	ValueDate       bizdate.BusinessDate
	ReversesEntryID *int64
}

// Account is the minimal account record the ledger needs.
type Account struct {
	ID          uuid.UUID
	ProductCode string
	Currency    money.Currency
	OpenedOn    bizdate.BusinessDate
}

// Balances are the three views FR-L4 requires. Ledger is derived from entries;
// Available subtracts unexpired holds; Pending is the hold total.
type Balances struct {
	AccountID uuid.UUID
	Currency  money.Currency
	Scale     uint8
	AsOf      bizdate.BusinessDate
	Ledger    int64
	Available int64
	Pending   int64
}

func toDate(b bizdate.BusinessDate) time.Time {
	return time.Date(b.Y, b.M, b.D, 0, 0, 0, 0, time.UTC)
}

func fromDate(t time.Time) bizdate.BusinessDate {
	return bizdate.BusinessDate{Y: t.Year(), M: t.Month(), D: t.Day()}
}
