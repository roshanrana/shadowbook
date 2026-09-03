//go:build integration

package posting_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

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
		t.Fatalf("reset: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func seed(t *testing.T, st *store.Store) (a, b uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	a = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	for _, id := range []uuid.UUID{a, b} {
		if err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: id, ProductCode: "CHK-01", Currency: "USD",
			OpenedOn: bizdate.Date(2018, time.June, 1),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return a, b
}

func req(a, b uuid.UUID, key string, amount int64) posting.Request {
	return posting.Request{
		Principal: "sim", IdempotencyKey: key, Kind: "transfer", Currency: "USD",
		BusinessDate: bizdate.Date(2028, time.February, 29),
		ValueDate:    bizdate.Date(2028, time.February, 29),
		PostedAt:     time.Date(2028, time.February, 29, 16, 59, 59, 0, time.UTC),
		Entries: []posting.EntryRequest{
			{AccountID: a, AmountMinor: -amount},
			{AccountID: b, AmountMinor: amount},
		},
	}
}

func TestPostHappyPath(t *testing.T) {
	st := newStore(t)
	a, b := seed(t, st)
	svc := posting.New(st)
	ctx := context.Background()

	res, err := svc.Post(ctx, req(a, b, "k1", 125000))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.Replayed {
		t.Fatal("first post reported as a replay")
	}
	if len(res.EntryIDs) != 2 {
		t.Fatalf("entry ids = %v", res.EntryIDs)
	}
	if res.PostingID != posting.PostingIDFor("sim", "k1") {
		t.Fatal("posting id is not the deterministic derivation")
	}

	bal, err := store.LedgerBalance(ctx, st.Pool, a, bizdate.Date(2028, time.February, 29))
	if err != nil || bal != -125000 {
		t.Fatalf("balance = %d, %v; want -125000", bal, err)
	}

	inv, err := store.GlobalInvariant(ctx, st.Pool)
	if err != nil || inv["USD"] != 0 {
		t.Fatalf("invariant = %v, %v", inv, err)
	}

	depth, err := store.OutboxDepth(ctx, st.Pool)
	if err != nil || depth != 1 {
		t.Fatalf("outbox depth = %d, %v; want 1 (co-committed with the entries)", depth, err)
	}
}

// Posting ids are a deterministic function of (principal, key) so the same seed
// yields byte-identical output (NFR-5).
func TestPostingIDIsDeterministic(t *testing.T) {
	one := posting.PostingIDFor("sim", "k1")
	two := posting.PostingIDFor("sim", "k1")
	other := posting.PostingIDFor("sim", "k2")
	another := posting.PostingIDFor("other", "k1")
	if one != two {
		t.Fatal("same inputs produced different ids")
	}
	if one == other || one == another {
		t.Fatal("different inputs collided")
	}
}

func TestReplaySameBodyReturnsIdenticalResponse(t *testing.T) {
	st := newStore(t)
	a, b := seed(t, st)
	svc := posting.New(st)
	ctx := context.Background()

	first, err := svc.Post(ctx, req(a, b, "k1", 125000))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Post(ctx, req(a, b, "k1", 125000))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed {
		t.Fatal("second post was not marked as a replay")
	}
	if second.PostingID != first.PostingID {
		t.Fatalf("replay posting id = %s, want %s", second.PostingID, first.PostingID)
	}
	if len(second.EntryIDs) != len(first.EntryIDs) || second.EntryIDs[0] != first.EntryIDs[0] {
		t.Fatalf("replay entry ids = %v, want %v", second.EntryIDs, first.EntryIDs)
	}

	var n int64
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("entry rows = %d after a replay, want 2 -- the replay wrote a second effect", n)
	}
}

func TestReplayDifferentBodyIsRejected(t *testing.T) {
	st := newStore(t)
	a, b := seed(t, st)
	svc := posting.New(st)
	ctx := context.Background()

	if _, err := svc.Post(ctx, req(a, b, "k1", 125000)); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Post(ctx, req(a, b, "k1", 999)) // same key, different amount
	if !errors.Is(err, posting.ErrIdempotencyBodyMismatch) {
		t.Fatalf("err = %v, want ErrIdempotencyBodyMismatch", err)
	}
}

// Named adversarial scenario: N concurrent same-key requests -> one effect.
func TestIdempotencyRace64(t *testing.T) {
	st := newStore(t)
	a, b := seed(t, st)
	svc := posting.New(st)
	ctx := context.Background()

	const n = 64
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []posting.Result
		errs    []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once
			res, err := svc.Post(ctx, req(a, b, "race", 4200))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			results = append(results, res)
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("%d of %d concurrent requests failed; first: %v", len(errs), n, errs[0])
	}
	if len(results) != n {
		t.Fatalf("got %d results, want %d", len(results), n)
	}

	want := results[0]
	for i, r := range results {
		if r.PostingID != want.PostingID {
			t.Fatalf("result %d posting id = %s, want %s", i, r.PostingID, want.PostingID)
		}
		if len(r.EntryIDs) != 2 || r.EntryIDs[0] != want.EntryIDs[0] || r.EntryIDs[1] != want.EntryIDs[1] {
			t.Fatalf("result %d entry ids = %v, want %v", i, r.EntryIDs, want.EntryIDs)
		}
	}

	var entries, postings, outbox int64
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&entries)
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM postings`).Scan(&postings)
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&outbox)
	if postings != 1 || entries != 2 || outbox != 1 {
		t.Fatalf("after %d concurrent identical requests: postings=%d entries=%d outbox=%d; want 1/2/1",
			n, postings, entries, outbox)
	}

	inv, _ := store.GlobalInvariant(ctx, st.Pool)
	if inv["USD"] != 0 {
		t.Fatalf("invariant broken under concurrency: %v", inv)
	}
}

func TestValidationRejectsBadRequests(t *testing.T) {
	a := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	for _, tc := range []struct {
		name    string
		mutate  func(*posting.Request)
		wantErr error
	}{
		{"no idempotency key", func(r *posting.Request) { r.IdempotencyKey = "" }, posting.ErrMissingIdempotencyKey},
		{"unknown kind", func(r *posting.Request) { r.Kind = "wizardry" }, posting.ErrUnknownKind},
		{"one entry", func(r *posting.Request) { r.Entries = r.Entries[:1] }, posting.ErrEntriesNotBalanced},
		{"unbalanced", func(r *posting.Request) { r.Entries[1].AmountMinor = 999 }, posting.ErrEntriesNotBalanced},
		{"unknown currency", func(r *posting.Request) { r.Currency = "XXX" }, money.ErrUnknownCurrency},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req(a, b, "v", 100)
			tc.mutate(&r)
			if err := posting.Validate(r); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// The database is the authority even if validation were bypassed: a zero-sum
// violation must surface from COMMIT, not be silently accepted.
func TestDatabaseRejectsUnbalancedEvenIfValidationBypassed(t *testing.T) {
	st := newStore(t)
	a, b := seed(t, st)
	ctx := context.Background()

	tx, err := st.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	pid := uuid.New()
	if err := store.InsertPosting(ctx, tx, store.Posting{
		ID: pid, Principal: "sim", Kind: "transfer", Currency: "USD",
		BusinessDate: bizdate.Date(2028, time.February, 29),
		ValueDate:    bizdate.Date(2028, time.February, 29),
		PostedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	amtA, _ := money.New(-100, "USD")
	amtB, _ := money.New(999, "USD")
	if _, err := store.InsertEntries(ctx, tx, []store.Entry{
		{PostingID: pid, AccountID: a, Amount: amtA, BusinessDate: bizdate.Date(2028, time.February, 29), ValueDate: bizdate.Date(2028, time.February, 29)},
		{PostingID: pid, AccountID: b, Amount: amtB, BusinessDate: bizdate.Date(2028, time.February, 29), ValueDate: bizdate.Date(2028, time.February, 29)},
	}); err != nil {
		t.Fatal(err)
	}
	err = tx.Commit(ctx)
	if err == nil {
		t.Fatal("unbalanced posting committed; the deferred trigger did not fire")
	}
	if !store.IsInvariantViolation(err) {
		t.Fatalf("commit error = %v, want a P0001 invariant violation", err)
	}
}

func TestBodyHashIsStableAndDiscriminating(t *testing.T) {
	a := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	r := req(a, b, "k", 100)

	h1, h2 := posting.BodyHash(r), posting.BodyHash(r)
	if fmt.Sprintf("%x", h1) != fmt.Sprintf("%x", h2) {
		t.Fatal("BodyHash is not stable across calls")
	}
	// The key itself is NOT part of the hash: the hash answers "is this the
	// same body", and the key is the question, not the answer.
	r2 := r
	r2.IdempotencyKey = "different"
	if fmt.Sprintf("%x", posting.BodyHash(r2)) != fmt.Sprintf("%x", h1) {
		t.Fatal("BodyHash must not depend on the idempotency key")
	}
	r3 := req(a, b, "k", 101)
	if fmt.Sprintf("%x", posting.BodyHash(r3)) == fmt.Sprintf("%x", h1) {
		t.Fatal("BodyHash did not change when the amount changed")
	}
}
