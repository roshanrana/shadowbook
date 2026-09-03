//go:build integration

package outbox_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/broker"
	"github.com/roshanrana/shadowbook/internal/ledger/outbox"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

const topic = "shadowbook.postings.v1"

func setup(t *testing.T) (*store.Store, *posting.Service, []uuid.UUID) {
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
	accounts := []uuid.UUID{
		uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}
	for _, id := range accounts {
		if err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: id, ProductCode: "CHK-01", Currency: "USD",
			OpenedOn: bizdate.Date(2018, time.June, 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st, posting.New(st), accounts
}

func post(t *testing.T, svc *posting.Service, acc []uuid.UUID, key string, amount int64) {
	t.Helper()
	_, err := svc.Post(context.Background(), posting.Request{
		Principal: "sim", IdempotencyKey: key, Kind: "transfer", Currency: "USD",
		BusinessDate: bizdate.Date(2028, time.February, 29),
		ValueDate:    bizdate.Date(2028, time.February, 29),
		PostedAt:     time.Date(2028, time.February, 29, 10, 0, 0, 0, time.UTC),
		Entries: []posting.EntryRequest{
			{AccountID: acc[0], AmountMinor: -amount},
			{AccountID: acc[1], AmountMinor: amount},
		},
	})
	if err != nil {
		t.Fatalf("post %s: %v", key, err)
	}
}

func TestDrainProducesAndMarksSent(t *testing.T) {
	st, svc, acc := setup(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		post(t, svc, acc, string(rune('a'+i)), int64(100+i))
	}

	fake := broker.NewFake()
	relay := outbox.New(st, fake, outbox.Options{Topic: topic})

	n, err := relay.Drain(ctx)
	if err != nil || n != 5 {
		t.Fatalf("drain = %d, %v; want 5", n, err)
	}
	if got := len(fake.Records(topic)); got != 5 {
		t.Fatalf("produced %d records, want 5", got)
	}
	depth, _ := store.OutboxDepth(ctx, st.Pool)
	if depth != 0 {
		t.Fatalf("outbox depth = %d after a successful drain", depth)
	}
	// A second drain has nothing to do.
	if n, err := relay.Drain(ctx); err != nil || n != 0 {
		t.Fatalf("second drain = %d, %v", n, err)
	}
}

// At-least-once by construction: rows are marked sent only after every produce
// is acknowledged, so a broker failure mid-batch replays rather than loses.
func TestAFailedProduceLeavesTheBatchUnsent(t *testing.T) {
	st, svc, acc := setup(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		post(t, svc, acc, string(rune('a'+i)), int64(100+i))
	}

	fake := broker.NewFake()
	fake.FailProduceAfter = 2 // dies partway through the batch
	relay := outbox.New(st, fake, outbox.Options{Topic: topic})

	if _, err := relay.Drain(ctx); err == nil {
		t.Fatal("a failed produce was reported as success")
	}
	depth, _ := store.OutboxDepth(ctx, st.Pool)
	if depth != 4 {
		t.Fatalf("outbox depth = %d after a failed produce, want all 4 still unsent", depth)
	}

	// Recover: the whole batch is retried. Some records reach the broker twice,
	// which is exactly why the consumer has an inbox.
	fake.FailProduceAfter = 0
	if n, err := relay.Drain(ctx); err != nil || n != 4 {
		t.Fatalf("retry = %d, %v; want 4", n, err)
	}
	if got := len(fake.Records(topic)); got < 4 {
		t.Fatalf("produced %d records after recovery, want at least 4", got)
	}
	depth, _ = store.OutboxDepth(ctx, st.Pool)
	if depth != 0 {
		t.Fatalf("depth = %d after recovery", depth)
	}
}

// Ordering per account is what makes the key choice load-bearing.
func TestRecordsAreKeyedByAccountAndOrdered(t *testing.T) {
	st, svc, acc := setup(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		post(t, svc, acc, string(rune('a'+i)), int64(100+i))
	}
	fake := broker.NewFake()
	if _, err := outbox.New(st, fake, outbox.Options{Topic: topic}).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	records := fake.Records(topic)
	for i, r := range records {
		if r.Key != acc[0].String() {
			t.Fatalf("record %d key = %q, want the account id", i, r.Key)
		}
		if len(r.Value) == 0 {
			t.Fatalf("record %d has an empty payload", i)
		}
		if i > 0 && r.Offset <= records[i-1].Offset {
			t.Fatalf("records are out of order at %d", i)
		}
	}
}

func TestBatchSizeIsRespected(t *testing.T) {
	st, svc, acc := setup(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		post(t, svc, acc, string(rune('a'+i)), int64(100+i))
	}
	relay := outbox.New(st, broker.NewFake(), outbox.Options{Topic: topic, BatchSize: 3})
	n, err := relay.Drain(ctx)
	if err != nil || n != 3 {
		t.Fatalf("drain = %d, %v; want 3", n, err)
	}
	depth, _ := store.OutboxDepth(ctx, st.Pool)
	if depth != 4 {
		t.Fatalf("depth = %d, want 4 remaining", depth)
	}
}

// Shutdown drains what is committed rather than stranding it (FR-L10).
func TestRunDrainsOnCancellation(t *testing.T) {
	st, svc, acc := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 3; i++ {
		post(t, svc, acc, string(rune('a'+i)), int64(100+i))
	}
	fake := broker.NewFake()
	relay := outbox.New(st, fake, outbox.Options{Topic: topic, Interval: time.Hour})

	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if got := len(fake.Records(topic)); got != 3 {
		t.Fatalf("shutdown stranded committed postings: produced %d of 3", got)
	}
}
