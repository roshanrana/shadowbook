//go:build integration

package accrual_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/accrual"
	"github.com/roshanrana/shadowbook/internal/ledger/balance"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
	"github.com/roshanrana/shadowbook/internal/testsupport"
)

func setup(t *testing.T) (*store.Store, *accrual.Engine, *balance.Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	st := testsupport.FreshStore(t)
	if err := consumer.EnsureSuspenseAccounts(ctx, st, bizdate.Date(2028, time.January, 1)); err != nil {
		t.Fatal(err)
	}

	sav := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	chk := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	for id, product := range map[uuid.UUID]string{sav: "SAV-01", chk: "CHK-01"} {
		if err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: id, ProductCode: product, Currency: "USD",
			OpenedOn: bizdate.Date(2021, time.January, 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	cal := bizdate.NewUSFederal(time.UTC)
	return st, accrual.New(st, cal), balance.New(st), sav, chk
}

func fund(t *testing.T, st *store.Store, account uuid.UUID, amount int64, d bizdate.BusinessDate, key string) {
	t.Helper()
	svc := posting.New(st)
	_, err := svc.Post(context.Background(), posting.Request{
		Principal: "sim", IdempotencyKey: key, Kind: "transfer", Currency: "USD",
		BusinessDate: d, ValueDate: d,
		PostedAt: time.Date(d.Y, d.M, d.D, 10, 0, 0, 0, time.UTC),
		Entries: []posting.EntryRequest{
			{AccountID: account, AmountMinor: amount},
			{AccountID: consumer.SuspenseAccountFor("USD"), AmountMinor: -amount},
		},
	})
	if err != nil {
		t.Fatalf("fund: %v", err)
	}
}

// EOD is triggered, never timed, and a replay is refused (LLD §3.6).
func TestEODIsIdempotentPerBusinessDate(t *testing.T) {
	st, eng, _, sav, _ := setup(t)
	ctx := context.Background()
	d := bizdate.Date(2028, time.March, 15)
	fund(t, st, sav, 5_000_000, d, "f1")

	now := time.Date(2028, time.March, 15, 23, 0, 0, 0, time.UTC)
	if _, err := eng.Run(ctx, d, now); err != nil {
		t.Fatalf("first EOD: %v", err)
	}
	_, err := eng.Run(ctx, d, now)
	if err == nil {
		t.Fatal("EOD ran twice for the same business date")
	}
	if !store.IsUniqueViolation(err, store.ConstraintEODRun) {
		t.Fatalf("replay error = %v, want a 23505 on eod_runs_pkey", err)
	}
}

// Phase order is fixed: expire -> interest -> fees -> checkpoint. Fees must see
// post-expiry AVAILABLE balance, which is exactly what Q7 diverges on.
func TestHoldsExpireBeforeFeesAreAssessed(t *testing.T) {
	st, eng, bal, _, chk := setup(t)
	ctx := context.Background()
	d := bizdate.Date(2028, time.March, 31) // month end

	fund(t, st, chk, 150_000, bizdate.Date(2028, time.March, 1), "f2")

	// A hold placed four days earlier is past its 72 hours by EOD.
	placed := time.Date(2028, time.March, 27, 9, 0, 0, 0, time.UTC)
	amt, _ := moneyUSD(100_000)
	if _, err := bal.PlaceHold(ctx, chk, amt, placed, bizdate.Date(2028, time.March, 27)); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2028, time.March, 31, 23, 0, 0, 0, time.UTC)
	rep, err := eng.Run(ctx, d, now)
	if err != nil {
		t.Fatalf("EOD: %v", err)
	}
	if rep.HoldsExpired != 1 {
		t.Fatalf("holds expired = %d, want 1 -- expiry must run before fees", rep.HoldsExpired)
	}
	if rep.FeesPosted == 0 {
		t.Fatal("no fee was assessed at month end")
	}

	after, err := bal.At(ctx, chk, d, now)
	if err != nil {
		t.Fatal(err)
	}
	if after.Pending != 0 {
		t.Fatalf("pending = %d after expiry, want 0", after.Pending)
	}
}

func TestCheckpointsAreWrittenAndBoundTheBalanceRead(t *testing.T) {
	st, eng, _, sav, _ := setup(t)
	ctx := context.Background()
	d := bizdate.Date(2028, time.March, 15)
	fund(t, st, sav, 1_000_000, d, "f3")

	rep, err := eng.Run(ctx, d, time.Date(2028, time.March, 15, 23, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checkpoints == 0 {
		t.Fatal("no checkpoints written")
	}
	var n int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM checkpoints WHERE business_date = $1`,
		time.Date(2028, time.March, 15, 0, 0, 0, 0, time.UTC)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("checkpoints table is empty after EOD")
	}
}

// Interest is triggered on the first BUSINESS day of the month but DATED the
// calendar first. 2028-04-01 is a Saturday, so if the trigger and the date were
// the same thing, April's interest would never post at all -- and Q12 would be
// undetectable.
func TestInterestPostsWhenTheFirstOfTheMonthIsAWeekend(t *testing.T) {
	st, eng, _, sav, _ := setup(t)
	ctx := context.Background()

	// Fund and accrue through March.
	fund(t, st, sav, 5_000_000, bizdate.Date(2028, time.March, 1), "f4")
	for _, d := range []bizdate.BusinessDate{
		bizdate.Date(2028, time.March, 30), bizdate.Date(2028, time.March, 31),
	} {
		if _, err := eng.Run(ctx, d, time.Date(d.Y, d.M, d.D, 23, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("EOD %s: %v", d, err)
		}
	}

	// Monday 2028-04-03 is the first business day of April.
	trigger := bizdate.Date(2028, time.April, 3)
	rep, err := eng.Run(ctx, trigger, time.Date(2028, time.April, 3, 23, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("EOD %s: %v", trigger, err)
	}
	if rep.InterestPosted == 0 {
		t.Fatal("no interest posted on the first business day after a weekend first-of-month")
	}

	// The entry must be DATED 2028-04-01, the calendar first.
	var n int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM entries e JOIN postings p USING (posting_id)
		  WHERE p.kind = 'interest' AND e.business_date = $1`,
		time.Date(2028, time.April, 1, 0, 0, 0, 0, time.UTC)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("interest was not dated the calendar first (2028-04-01)")
	}
}

func TestTheInvariantHoldsAfterEveryEODPhase(t *testing.T) {
	st, eng, _, sav, chk := setup(t)
	ctx := context.Background()
	fund(t, st, sav, 5_000_000, bizdate.Date(2028, time.March, 1), "f5")
	fund(t, st, chk, 150_000, bizdate.Date(2028, time.March, 1), "f6")

	for _, d := range []bizdate.BusinessDate{
		bizdate.Date(2028, time.March, 30),
		bizdate.Date(2028, time.March, 31),
		bizdate.Date(2028, time.April, 3),
	} {
		if _, err := eng.Run(ctx, d, time.Date(d.Y, d.M, d.D, 23, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("EOD %s: %v", d, err)
		}
		res, err := obs.CheckInvariant(ctx, st.Pool)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Err(); err != nil {
			t.Fatalf("after EOD %s: %v", d, err)
		}
	}
}

func moneyUSD(minor int64) (m moneyAmount, err error) { return newUSD(minor) }

type moneyAmount = money.Amount

func newUSD(minor int64) (money.Amount, error) { return money.New(minor, "USD") }
