//go:build integration

package consumer_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	shadowbookv1 "github.com/roshanrana/shadowbook/gen/go/shadowbook/v1"
	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/broker"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

const topic = "shadowbook.movements.v1"

func newStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("SHADOWBOOK_LEDGER_DSN")
	if dsn == "" {
		t.Skip("SHADOWBOOK_LEDGER_DSN unset")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
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
	return st
}

func seedAccount(t *testing.T, st *store.Store) uuid.UUID {
	t.Helper()
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if err := store.InsertAccount(context.Background(), st.Pool, store.Account{
		ID: id, ProductCode: "CHK-01", Currency: "USD", OpenedOn: bizdate.Date(2018, time.June, 1),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func movement(t *testing.T, account uuid.UUID, msgID string, minor int64) broker.Record {
	t.Helper()
	ev := &shadowbookv1.MovementEvent{
		MessageId: msgID, AccountId: account.String(),
		Amount:       &shadowbookv1.Money{Minor: minor, Currency: "USD", Scale: 2},
		BusinessDate: "2028-02-29", ValueDate: "2028-02-29",
		PostedAt: "2028-02-29T10:00:00Z", Kind: "transfer",
	}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return broker.Record{Topic: topic, Key: account.String(), Value: b}
}

func countEntries(t *testing.T, st *store.Store, account uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM entries WHERE account_id = $1`, account).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertInvariant(t *testing.T, st *store.Store) {
	t.Helper()
	res, err := obs.CheckInvariant(context.Background(), st.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Err(); err != nil {
		t.Fatalf("global invariant broken: %v", err)
	}
}

// Every mode books a movement against the per-currency suspense account, so
// double entry -- and therefore the global invariant -- holds by construction.
func TestAllModesPreserveTheGlobalInvariant(t *testing.T) {
	for _, mode := range []consumer.Mode{
		consumer.AtMostOnce, consumer.AtLeastOnce, consumer.InboxDedup, consumer.Transactional,
	} {
		t.Run(string(mode), func(t *testing.T) {
			st := newStore(t)
			acct := seedAccount(t, st)
			fake := broker.NewFake()
			ctx := context.Background()

			if err := fake.Produce(ctx, []broker.Record{
				movement(t, acct, "m-1", 1000),
				movement(t, acct, "m-2", -400),
			}); err != nil {
				t.Fatal(err)
			}
			c, err := consumer.New(st, fake, consumer.Options{Mode: mode, Topic: topic})
			if err != nil {
				t.Fatal(err)
			}
			stats, err := c.RunOnce(ctx)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if stats.Applied != 2 {
				t.Fatalf("applied = %d, want 2", stats.Applied)
			}
			if got := countEntries(t, st, acct); got != 2 {
				t.Fatalf("entries = %d, want 2", got)
			}
			assertInvariant(t, st)
		})
	}
}

// The ablation table's central claim: under redelivery, B duplicates and C does
// not. This is the property Finding 2 measures, demonstrated here deterministically
// rather than by killing a broker.
func TestRedeliveryDuplicatesInBButNotInC(t *testing.T) {
	for _, tc := range []struct {
		mode           consumer.Mode
		wantEntries    int64
		wantDuplicates int
		why            string
	}{
		{consumer.AtLeastOnce, 4, 0, "no dedup: the redelivered batch is applied a second time"},
		{consumer.InboxDedup, 2, 2, "inbox_pkey suppresses the redelivery"},
		{consumer.Transactional, 2, 2, "as C, plus transactional offsets"},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			st := newStore(t)
			acct := seedAccount(t, st)
			fake := broker.NewFake()
			ctx := context.Background()

			if err := fake.Produce(ctx, []broker.Record{
				movement(t, acct, "m-1", 1000),
				movement(t, acct, "m-2", -400),
			}); err != nil {
				t.Fatal(err)
			}
			c, err := consumer.New(st, fake, consumer.Options{Mode: tc.mode, Topic: topic})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}

			// The broker replays the batch: an uncommitted offset after a restart.
			fake.Rewind(topic, 0)
			second, err := c.RunOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if got := countEntries(t, st, acct); got != tc.wantEntries {
				t.Fatalf("%s: entries after redelivery = %d, want %d (%s)",
					tc.mode, got, tc.wantEntries, tc.why)
			}
			if second.Duplicates != tc.wantDuplicates {
				t.Fatalf("%s: duplicates suppressed = %d, want %d",
					tc.mode, second.Duplicates, tc.wantDuplicates)
			}
			// Even mode B, which double-books, keeps the invariant: both legs
			// are written. Duplication is a correctness failure that the
			// zero-sum invariant cannot catch -- which is the point of
			// reconciling at all.
			assertInvariant(t, st)
		})
	}
}

// Mode A commits offsets before applying, so a failure after the commit loses
// the batch outright. Nothing else in the system will notice.
func TestModeALosesRecordsOnFailureAfterCommit(t *testing.T) {
	st := newStore(t)
	acct := seedAccount(t, st)
	fake := broker.NewFake()
	ctx := context.Background()

	if err := fake.Produce(ctx, []broker.Record{movement(t, acct, "m-1", 1000)}); err != nil {
		t.Fatal(err)
	}
	// A record whose payload cannot be decoded stands in for the apply failing
	// after the offset has already been committed.
	if err := fake.Produce(ctx, []broker.Record{{Topic: topic, Key: acct.String(), Value: []byte{0xff, 0xff}}}); err != nil {
		t.Fatal(err)
	}
	c, err := consumer.New(st, fake, consumer.Options{Mode: consumer.AtMostOnce, Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunOnce(ctx); err == nil {
		t.Fatal("expected the apply to fail")
	}

	// Offsets were already committed, so a re-poll returns nothing: the good
	// record was applied, the bad one is gone forever.
	again, err := c.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.Polled != 0 {
		t.Fatalf("mode A re-polled %d records; offsets were committed before applying", again.Polled)
	}
	assertInvariant(t, st)
}

func TestUnknownModeIsRejected(t *testing.T) {
	if _, err := consumer.New(nil, nil, consumer.Options{Mode: "Z"}); err == nil {
		t.Fatal("mode Z was accepted")
	}
	for _, m := range []consumer.Mode{"A", "B", "C", "D"} {
		if !m.Valid() {
			t.Fatalf("mode %s reported invalid", m)
		}
	}
}

func TestSuspenseAccountIsDeterministicPerCurrency(t *testing.T) {
	if consumer.SuspenseAccountFor("USD") != consumer.SuspenseAccountFor("USD") {
		t.Fatal("suspense account is not stable")
	}
	if consumer.SuspenseAccountFor("USD") == consumer.SuspenseAccountFor("JPY") {
		t.Fatal("USD and JPY share a suspense account")
	}
}

func TestApplyRejectsMalformedMovements(t *testing.T) {
	st := newStore(t)
	acct := seedAccount(t, st)
	ctx := context.Background()

	bad := func(mutate func(*shadowbookv1.MovementEvent)) broker.Record {
		ev := &shadowbookv1.MovementEvent{
			MessageId: "m-x", AccountId: acct.String(),
			Amount:       &shadowbookv1.Money{Minor: 100, Currency: "USD", Scale: 2},
			BusinessDate: "2028-02-29", ValueDate: "2028-02-29",
			PostedAt: "2028-02-29T10:00:00Z", Kind: "transfer",
		}
		mutate(ev)
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		return broker.Record{Topic: topic, Key: acct.String(), Value: b}
	}

	for _, tc := range []struct {
		name string
		rec  broker.Record
	}{
		{"no message id", bad(func(e *shadowbookv1.MovementEvent) { e.MessageId = "" })},
		{"bad account id", bad(func(e *shadowbookv1.MovementEvent) { e.AccountId = "not-a-uuid" })},
		{"unknown currency", bad(func(e *shadowbookv1.MovementEvent) { e.Amount.Currency = "XXX" })},
		{"bad business date", bad(func(e *shadowbookv1.MovementEvent) { e.BusinessDate = "29/02/2028" })},
		{"bad value date", bad(func(e *shadowbookv1.MovementEvent) { e.ValueDate = "nope" })},
		{"bad posted_at", bad(func(e *shadowbookv1.MovementEvent) { e.PostedAt = "yesterday" })},
		{"undecodable payload", broker.Record{Topic: topic, Key: acct.String(), Value: []byte{0xff, 0xfe}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := broker.NewFake()
			if err := fake.Produce(ctx, []broker.Record{tc.rec}); err != nil {
				t.Fatal(err)
			}
			c, err := consumer.New(st, fake, consumer.Options{Mode: consumer.InboxDedup, Topic: topic})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.RunOnce(ctx); err == nil {
				t.Fatalf("a malformed movement (%s) was applied", tc.name)
			}
			assertInvariant(t, st)
		})
	}
}

func TestRunConsumesUntilCancelled(t *testing.T) {
	st := newStore(t)
	acct := seedAccount(t, st)
	fake := broker.NewFake()
	ctx, cancel := context.WithCancel(context.Background())

	if err := fake.Produce(ctx, []broker.Record{
		movement(t, acct, "r-1", 100), movement(t, acct, "r-2", -100),
	}); err != nil {
		t.Fatal(err)
	}
	c, err := consumer.New(st, fake, consumer.Options{Mode: consumer.InboxDedup, Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.After(10 * time.Second)
	for countEntries(t, st, acct) < 2 {
		select {
		case <-deadline:
			t.Fatal("Run never applied the pending movements")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop on cancellation")
	}
	assertInvariant(t, st)
}

func TestRunStopsCleanlyOnAClosedBroker(t *testing.T) {
	st := newStore(t)
	fake := broker.NewFake()
	_ = fake.Close()
	c, err := consumer.New(st, fake, consumer.Options{Mode: consumer.InboxDedup, Topic: topic})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("a closed broker should end the loop cleanly, got %v", err)
	}
}

func TestEnsureSuspenseAccountsIsRepeatable(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := consumer.EnsureSuspenseAccounts(ctx, st, bizdate.Date(2028, time.January, 1)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	var n int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM accounts WHERE product_code = 'SUSPENSE'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("suspense accounts = %d, want 3 (one per currency)", n)
	}
}
