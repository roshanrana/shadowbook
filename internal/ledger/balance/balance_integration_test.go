//go:build integration

package balance_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/balance"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

func setup(t *testing.T) (*store.Store, *balance.Service, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("SHADOWBOOK_LEDGER_DSN")
	if dsn == "" {
		t.Skip("SHADOWBOOK_LEDGER_DSN unset")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := consumer.EnsureSuspenseAccounts(ctx, st, bizdate.Date(2028, time.January, 1)); err != nil {
		t.Fatal(err)
	}
	acc := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if err := store.InsertAccount(ctx, st.Pool, store.Account{
		ID: acc, ProductCode: "CHK-01", Currency: "USD",
		OpenedOn: bizdate.Date(2018, time.June, 1),
	}); err != nil {
		t.Fatal(err)
	}
	return st, balance.New(st), acc
}

func fund(t *testing.T, st *store.Store, acc uuid.UUID, amount int64, key string) {
	t.Helper()
	d := bizdate.Date(2028, time.February, 29)
	if _, err := posting.New(st).Post(context.Background(), posting.Request{
		Principal: "sim", IdempotencyKey: key, Kind: "transfer", Currency: "USD",
		BusinessDate: d, ValueDate: d,
		PostedAt: time.Date(2028, time.February, 29, 10, 0, 0, 0, time.UTC),
		Entries: []posting.EntryRequest{
			{AccountID: acc, AmountMinor: amount},
			{AccountID: consumer.SuspenseAccountFor("USD"), AmountMinor: -amount},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

var (
	asOf = bizdate.Date(2028, time.February, 29)
	now  = time.Date(2028, time.February, 29, 12, 0, 0, 0, time.UTC)
)

func TestAnEmptyAccountReadsZeroNotAnError(t *testing.T) {
	_, bal, acc := setup(t)
	b, err := bal.At(context.Background(), acc, asOf, now)
	if err != nil {
		t.Fatalf("an account with no entries and no checkpoint: %v", err)
	}
	if b.Ledger != 0 || b.Available != 0 || b.Pending != 0 {
		t.Fatalf("empty account = %+v", b)
	}
	if b.Scale != 2 || b.Currency != "USD" {
		t.Fatalf("scale/currency = %d/%s", b.Scale, b.Currency)
	}
}

// A hold reduces AVAILABLE and never LEDGER. Q7 is the legacy core assessing a
// fee on the wrong one of these two numbers.
func TestHoldsMoveAvailableNotLedger(t *testing.T) {
	st, bal, acc := setup(t)
	ctx := context.Background()
	fund(t, st, acc, 500_000, "f1")

	amt, _ := money.New(200_000, "USD")
	h, err := bal.PlaceHold(ctx, acc, amt, now, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if h.ExpiresAt.Sub(h.PlacedAt) != balance.HoldDuration {
		t.Fatalf("hold lasts %v, documented rule is %v", h.ExpiresAt.Sub(h.PlacedAt), balance.HoldDuration)
	}

	b, err := bal.At(ctx, acc, asOf, now)
	if err != nil {
		t.Fatal(err)
	}
	if b.Ledger != 500_000 {
		t.Fatalf("ledger = %d; a hold must not move it", b.Ledger)
	}
	if b.Available != 300_000 || b.Pending != 200_000 {
		t.Fatalf("available/pending = %d/%d, want 300000/200000", b.Available, b.Pending)
	}
}

func TestAHoldExpiresExactlySeventyTwoHoursAfterPlacement(t *testing.T) {
	st, bal, acc := setup(t)
	ctx := context.Background()
	fund(t, st, acc, 500_000, "f2")
	amt, _ := money.New(100_000, "USD")
	if _, err := bal.PlaceHold(ctx, acc, amt, now, asOf); err != nil {
		t.Fatal(err)
	}

	justBefore := now.Add(balance.HoldDuration - time.Second)
	b, _ := bal.At(ctx, acc, asOf, justBefore)
	if b.Pending != 100_000 {
		t.Fatalf("hold released early: pending = %d one second before expiry", b.Pending)
	}

	justAfter := now.Add(balance.HoldDuration + time.Second)
	b, _ = bal.At(ctx, acc, asOf, justAfter)
	if b.Pending != 0 {
		t.Fatalf("hold still pending one second after 72h: %d", b.Pending)
	}
}

func TestAHoldBeyondAvailableIsRefused(t *testing.T) {
	st, bal, acc := setup(t)
	ctx := context.Background()
	fund(t, st, acc, 100_000, "f3")
	amt, _ := money.New(200_000, "USD")
	_, err := bal.PlaceHold(ctx, acc, amt, now, asOf)
	if !errors.Is(err, balance.ErrInsufficientAvailable) {
		t.Fatalf("err = %v, want ErrInsufficientAvailable", err)
	}
	// A non-positive hold is nonsense, not a credit.
	zero, _ := money.New(0, "USD")
	if _, err := bal.PlaceHold(ctx, acc, zero, now, asOf); err == nil {
		t.Fatal("a zero hold was accepted")
	}
}

func TestReleaseAndExpiry(t *testing.T) {
	st, bal, acc := setup(t)
	ctx := context.Background()
	fund(t, st, acc, 500_000, "f4")
	amt, _ := money.New(50_000, "USD")
	h, err := bal.PlaceHold(ctx, acc, amt, now, asOf)
	if err != nil {
		t.Fatal(err)
	}

	if err := bal.ReleaseHold(ctx, h.ID, now, "captured"); err != nil {
		t.Fatal(err)
	}
	if err := bal.ReleaseHold(ctx, h.ID, now, "captured"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double release err = %v, want ErrNotFound", err)
	}
	if err := bal.ReleaseHold(ctx, h.ID, now, "nonsense"); err == nil {
		t.Fatal("an unknown release kind was accepted")
	}

	// ExpireDue closes everything past its expiry.
	h2, err := bal.PlaceHold(ctx, acc, amt, now, asOf)
	if err != nil {
		t.Fatal(err)
	}
	n, err := bal.ExpireDue(ctx, now.Add(balance.HoldDuration+time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired %d holds, want 1", n)
	}
	b, _ := bal.At(ctx, acc, asOf, now.Add(balance.HoldDuration+time.Hour))
	if b.Pending != 0 {
		t.Fatalf("pending = %d after expiry (hold %s)", b.Pending, h2.ID)
	}
}

// Checkpoints must not change the answer -- only how much work the read does.
func TestCheckpointPreservesTheBalance(t *testing.T) {
	st, bal, acc := setup(t)
	ctx := context.Background()
	fund(t, st, acc, 500_000, "f5")

	before, err := bal.At(ctx, acc, asOf, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := bal.Checkpoint(ctx, acc, asOf); err != nil {
		t.Fatal(err)
	}
	after, err := bal.At(ctx, acc, asOf, now)
	if err != nil {
		t.Fatal(err)
	}
	if before.Ledger != after.Ledger {
		t.Fatalf("checkpoint changed the balance: %d -> %d", before.Ledger, after.Ledger)
	}

	// Entries after the checkpoint still count.
	fund(t, st, acc, 25_000, "f6")
	later, err := bal.At(ctx, acc, asOf, now)
	if err != nil {
		t.Fatal(err)
	}
	if later.Ledger != after.Ledger+25_000 {
		t.Fatalf("post-checkpoint entry ignored: %d, want %d", later.Ledger, after.Ledger+25_000)
	}

	// Checkpoints are append-only: a second one for the same account-day fails.
	if err := bal.Checkpoint(ctx, acc, asOf); err == nil {
		t.Fatal("a duplicate checkpoint was accepted")
	}
}

func TestUnknownAccountIsNotFound(t *testing.T) {
	_, bal, _ := setup(t)
	_, err := bal.At(context.Background(), uuid.New(), asOf, now)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
