// Package balance answers the three balance questions of FR-L4.
//
// No column stores a balance. Ledger is derived from entries plus the latest
// checkpoint; available subtracts unexpired holds; pending is the hold total.
// Holds never touch the ledger balance -- Q7 is the legacy core assessing a
// minimum-balance fee on the ledger balance instead of the available one.
package balance

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

// HoldDuration is the documented hold lifetime: 72 hours after placement
// (FR-L5). Q8 expires at midnight on placement + 3 instead.
const HoldDuration = 72 * time.Hour

// Service reads balances and manages holds.
type Service struct{ st *store.Store }

// New builds the service.
func New(st *store.Store) *Service { return &Service{st: st} }

// At returns all three balances for an account as of a business date.
//
// `now` is passed in rather than read from the clock: hold expiry depends on an
// instant, and a balance that silently depended on wall-clock time would break
// determinism (NFR-5, LLD §8.3).
func (s *Service) At(ctx context.Context, accountID uuid.UUID, asOf bizdate.BusinessDate, now time.Time) (store.Balances, error) {
	acct, err := store.GetAccount(ctx, s.st.Pool, accountID)
	if err != nil {
		return store.Balances{}, err
	}
	ledger, err := store.LedgerBalance(ctx, s.st.Pool, accountID, asOf)
	if err != nil {
		return store.Balances{}, fmt.Errorf("balance: ledger: %w", err)
	}
	pending, err := store.OpenHoldTotal(ctx, s.st.Pool, accountID, now)
	if err != nil {
		return store.Balances{}, fmt.Errorf("balance: holds: %w", err)
	}
	zero, err := money.New(0, acct.Currency)
	if err != nil {
		return store.Balances{}, err
	}
	return store.Balances{
		AccountID: accountID, Currency: acct.Currency, Scale: zero.Scale, AsOf: asOf,
		Ledger: ledger, Available: ledger - pending, Pending: pending,
	}, nil
}

// PlaceHold reserves funds against available balance.
func (s *Service) PlaceHold(ctx context.Context, accountID uuid.UUID, amount money.Amount,
	placedAt time.Time, asOf bizdate.BusinessDate) (store.Hold, error) {
	if amount.Minor <= 0 {
		return store.Hold{}, fmt.Errorf("balance: hold amount must be positive, got %d", amount.Minor)
	}
	bal, err := s.At(ctx, accountID, asOf, placedAt)
	if err != nil {
		return store.Hold{}, err
	}
	if bal.Available < amount.Minor {
		return store.Hold{}, fmt.Errorf("%w: available %d, requested %d",
			ErrInsufficientAvailable, bal.Available, amount.Minor)
	}
	h := store.Hold{
		ID: uuid.New(), AccountID: accountID, Amount: amount,
		PlacedAt: placedAt.UTC(), ExpiresAt: placedAt.UTC().Add(HoldDuration),
	}
	if err := store.InsertHold(ctx, s.st.Pool, h); err != nil {
		return store.Hold{}, fmt.Errorf("balance: insert hold: %w", err)
	}
	return h, nil
}

// ReleaseHold closes a hold.
func (s *Service) ReleaseHold(ctx context.Context, id uuid.UUID, at time.Time, kind string) error {
	switch kind {
	case "captured", "cancelled", "expired":
	default:
		return fmt.Errorf("balance: unknown release kind %q", kind)
	}
	return store.ReleaseHold(ctx, s.st.Pool, id, at.UTC(), kind)
}

// ExpireDue releases every hold past its 72 hours. First EOD phase.
func (s *Service) ExpireDue(ctx context.Context, at time.Time) (int64, error) {
	return store.ExpireHoldsAsOf(ctx, s.st.Pool, at.UTC())
}

// Checkpoint writes the account-day balance checkpoint. Checkpoints are what
// keep the derived balance read bounded as entries accumulate (NFR-2).
func (s *Service) Checkpoint(ctx context.Context, accountID uuid.UUID, d bizdate.BusinessDate) error {
	acct, err := store.GetAccount(ctx, s.st.Pool, accountID)
	if err != nil {
		return err
	}
	bal, err := store.LedgerBalance(ctx, s.st.Pool, accountID, d)
	if err != nil {
		return err
	}
	last, err := store.MaxEntryIDAsOf(ctx, s.st.Pool, accountID, d)
	if err != nil {
		return err
	}
	return store.InsertCheckpoint(ctx, s.st.Pool, accountID, d, acct.Currency, bal, last)
}
