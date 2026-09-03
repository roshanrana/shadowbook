// Package posting is the ledger's write path: idempotency, zero-sum entries and
// the transactional outbox, all in one database transaction.
//
// One service, both ingress paths. D-012 has legacy-sim reaching the ledger
// over HTTP for Finding 1 and over the movement topic for Finding 2; both land
// here, so neither path can develop its own semantics. The contract test for
// that equivalence is risk R6's control.
package posting

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	shadowbookv1 "github.com/roshanrana/shadowbook/gen/go/shadowbook/v1"
	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

// Errors the API maps to status codes. httpapi is the only package that knows
// about HTTP; these are the vocabulary it translates.
var (
	ErrIdempotencyBodyMismatch = errors.New("posting: same idempotency key, different body")
	ErrEntriesNotBalanced      = errors.New("posting: entries do not sum to zero, or fewer than two")
	ErrCurrencyMismatch        = errors.New("posting: entry currency differs from posting currency")
	ErrMissingIdempotencyKey   = errors.New("posting: idempotency key is required")
	ErrUnknownKind             = errors.New("posting: unknown posting kind")
)

// Valid posting kinds, matching the CHECK constraint in migration 0002.
var validKinds = map[string]bool{"transfer": true, "interest": true, "fee": true, "reversal": true}

// postingNamespace makes posting ids a deterministic function of
// (principal, idempotency key) rather than random.
//
// This is what lets NFR-5 hold end to end: the same seed produces the same
// legacy-sim stream, the same idempotency keys, and therefore byte-identical
// posting ids in the extracts and the findings. A random v4 id would make the
// Finding 1 table differ on every run for no reason.
var postingNamespace = uuid.MustParse("5f3a1c62-9d84-5b7e-9f21-8a4c6e0d17b3")

// PostingIDFor derives the deterministic posting id.
func PostingIDFor(principal, idemKey string) uuid.UUID {
	return uuid.NewSHA1(postingNamespace, []byte(principal+"/"+idemKey))
}

// EntryRequest is one requested leg.
type EntryRequest struct {
	AccountID   uuid.UUID `json:"account_id"`
	AmountMinor int64     `json:"amount_minor"`
}

// Request is a posting as submitted, over either ingress path.
type Request struct {
	Principal         string
	IdempotencyKey    string
	Kind              string
	Currency          money.Currency
	BusinessDate      bizdate.BusinessDate
	ValueDate         bizdate.BusinessDate
	PostedAt          time.Time
	ReversesPostingID *uuid.UUID
	Entries           []EntryRequest
}

// Result is what the caller gets back. Replayed marks a response served from
// the idempotency record rather than newly written.
type Result struct {
	PostingID    uuid.UUID            `json:"posting_id"`
	BusinessDate bizdate.BusinessDate `json:"-"`
	EntryIDs     []int64              `json:"entry_ids"`
	Replayed     bool                 `json:"-"`
}

// Service writes postings.
type Service struct {
	st *store.Store
}

// New builds the service.
func New(st *store.Store) *Service { return &Service{st: st} }

// BodyHash is the canonical hash of a request body, used to tell a genuine
// replay from a key reused with different content. Canonical means: the same
// logical request always hashes the same, regardless of field order or
// whitespace in the wire format, because it is hashed from the parsed struct.
func BodyHash(r Request) []byte {
	type canonical struct {
		Kind      string         `json:"kind"`
		Currency  string         `json:"currency"`
		Business  string         `json:"business_date"`
		Value     string         `json:"value_date"`
		PostedAt  string         `json:"posted_at"`
		Reverses  string         `json:"reverses_posting_id"`
		EntryList []EntryRequest `json:"entries"`
	}
	c := canonical{
		Kind: r.Kind, Currency: string(r.Currency),
		Business: r.BusinessDate.String(), Value: r.ValueDate.String(),
		PostedAt: r.PostedAt.UTC().Format(time.RFC3339Nano),
		// Entries are hashed in submitted order: order is part of the request.
		EntryList: r.Entries,
	}
	if r.ReversesPostingID != nil {
		c.Reverses = r.ReversesPostingID.String()
	}
	b, err := json.Marshal(c)
	if err != nil { // a struct of strings and ints cannot fail to marshal
		panic(fmt.Sprintf("posting: canonical marshal: %v", err))
	}
	sum := sha256.Sum256(b)
	return sum[:]
}

// Validate checks everything that can be checked without the database. The
// zero-sum rule is checked here AND in the database (LLD invariant 1): this
// gives a clear 422 instead of a COMMIT failure, but the constraint remains the
// authority.
func Validate(r Request) error {
	if r.IdempotencyKey == "" {
		return ErrMissingIdempotencyKey
	}
	if !validKinds[r.Kind] {
		return fmt.Errorf("%w: %q", ErrUnknownKind, r.Kind)
	}
	if len(r.Entries) < 2 {
		return fmt.Errorf("%w: %d entries", ErrEntriesNotBalanced, len(r.Entries))
	}
	amounts := make([]money.Amount, 0, len(r.Entries))
	for i, e := range r.Entries {
		a, err := money.New(e.AmountMinor, r.Currency)
		if err != nil {
			return fmt.Errorf("posting: entry %d: %w", i, err)
		}
		amounts = append(amounts, a)
	}
	sum, err := money.Sum(amounts...)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCurrencyMismatch, err)
	}
	if !sum.IsZero() {
		return fmt.Errorf("%w: sums to %d", ErrEntriesNotBalanced, sum.Minor)
	}
	return nil
}

// Post writes a posting, or replays a previous identical one.
//
// The duplicate is found by the unique-constraint violation on
// idempotency_keys_pkey -- never by a prior SELECT. That is what makes N
// concurrent requests with the same key produce exactly one effect
// deterministically rather than by luck.
func (s *Service) Post(ctx context.Context, r Request) (Result, error) {
	if err := Validate(r); err != nil {
		return Result{}, err
	}
	hash := BodyHash(r)
	postingID := PostingIDFor(r.Principal, r.IdempotencyKey)

	tx, err := s.st.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	// 1. Claim the key. A duplicate raises 23505 here.
	if err := store.ClaimIdempotencyKey(ctx, tx, r.Principal, r.IdempotencyKey, hash); err != nil {
		if store.IsUniqueViolation(err, store.ConstraintIdempotency) {
			_ = tx.Rollback(ctx)
			committed = true // the deferred rollback must not run twice
			return s.replay(ctx, r, hash)
		}
		return Result{}, fmt.Errorf("posting: claim idempotency key: %w", err)
	}

	// 2. The envelope.
	if err := store.InsertPosting(ctx, tx, store.Posting{
		ID: postingID, Principal: r.Principal, Kind: r.Kind, Currency: r.Currency,
		BusinessDate: r.BusinessDate, ValueDate: r.ValueDate,
		PostedAt: r.PostedAt.UTC(), ReversesPostingID: r.ReversesPostingID,
	}); err != nil {
		return Result{}, fmt.Errorf("posting: insert posting: %w", err)
	}

	// 3. The legs.
	entries := make([]store.Entry, 0, len(r.Entries))
	for _, e := range r.Entries {
		amt, err := money.New(e.AmountMinor, r.Currency)
		if err != nil {
			return Result{}, err
		}
		entries = append(entries, store.Entry{
			PostingID: postingID, AccountID: e.AccountID, Amount: amt,
			BusinessDate: r.BusinessDate, ValueDate: r.ValueDate,
		})
	}
	entryIDs, err := store.InsertEntries(ctx, tx, entries)
	if err != nil {
		return Result{}, fmt.Errorf("posting: insert entries: %w", err)
	}

	// 4. The outbox row, in the SAME transaction. This co-commit is the whole
	//    of the transactional-outbox pattern (FR-L7).
	payload, err := marshalPostingEvent(postingID, r, entryIDs)
	if err != nil {
		return Result{}, err
	}
	if err := store.InsertOutbox(ctx, tx, postingID, r.Entries[0].AccountID.String(), payload); err != nil {
		return Result{}, fmt.Errorf("posting: insert outbox: %w", err)
	}

	// 5. Store the response so a replay returns something identical.
	result := Result{PostingID: postingID, BusinessDate: r.BusinessDate, EntryIDs: entryIDs}
	respJSON, err := json.Marshal(result)
	if err != nil {
		return Result{}, fmt.Errorf("posting: marshal response: %w", err)
	}
	if err := store.CompleteIdempotency(ctx, tx, r.Principal, r.IdempotencyKey, postingID, respJSON); err != nil {
		return Result{}, fmt.Errorf("posting: complete idempotency: %w", err)
	}

	// 6. COMMIT. The deferred zero-sum trigger fires here.
	if err := tx.Commit(ctx); err != nil {
		if store.IsInvariantViolation(err) {
			return Result{}, fmt.Errorf("%w: %w", ErrEntriesNotBalanced, err)
		}
		return Result{}, fmt.Errorf("posting: commit: %w", err)
	}
	committed = true
	return result, nil
}

// replay serves a stored response, or reports a body mismatch.
func (s *Service) replay(ctx context.Context, r Request, hash []byte) (Result, error) {
	rec, err := store.LoadIdempotency(ctx, s.st.Pool, r.Principal, r.IdempotencyKey)
	if err != nil {
		return Result{}, fmt.Errorf("posting: load idempotency: %w", err)
	}
	if !bytesEqual(rec.BodyHash, hash) {
		return Result{}, ErrIdempotencyBodyMismatch
	}
	// A concurrent first request may hold the claim but not yet have written
	// its response. Wait briefly rather than returning an empty result: the
	// N-concurrent scenario requires all N callers to see the same answer.
	for attempt := 0; attempt < 50; attempt++ {
		if len(rec.Response) > 2 { // "{}" means not yet completed
			var out Result
			if err := json.Unmarshal(rec.Response, &out); err != nil {
				return Result{}, fmt.Errorf("posting: stored response: %w", err)
			}
			out.Replayed = true
			out.BusinessDate = r.BusinessDate
			return out, nil
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
		if rec, err = store.LoadIdempotency(ctx, s.st.Pool, r.Principal, r.IdempotencyKey); err != nil {
			return Result{}, fmt.Errorf("posting: reload idempotency: %w", err)
		}
	}
	return Result{}, errors.New("posting: concurrent request did not complete in time")
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func marshalPostingEvent(id uuid.UUID, r Request, entryIDs []int64) ([]byte, error) {
	ev := &shadowbookv1.PostingEvent{
		PostingId:    id.String(),
		Principal:    r.Principal,
		Kind:         r.Kind,
		BusinessDate: r.BusinessDate.String(),
		ValueDate:    r.ValueDate.String(),
		PostedAt:     r.PostedAt.UTC().Format(time.RFC3339Nano),
	}
	if r.ReversesPostingID != nil {
		ev.ReversesPostingId = r.ReversesPostingID.String()
	}
	scale, _ := money.New(0, r.Currency)
	for i, e := range r.Entries {
		var eid int64
		if i < len(entryIDs) {
			eid = entryIDs[i]
		}
		ev.Entries = append(ev.Entries, &shadowbookv1.Entry{
			EntryId:   eid,
			AccountId: e.AccountID.String(),
			Amount: &shadowbookv1.Money{
				Minor: e.AmountMinor, Currency: string(r.Currency), Scale: uint32(scale.Scale),
			},
		})
	}
	// Deterministic marshalling: the outbox payload is part of the byte-identical
	// output NFR-5 promises, so field order must not vary between runs.
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("posting: marshal event: %w", err)
	}
	return b, nil
}

// Ensure pgx.Tx satisfies store.Queryer at compile time.
var _ store.Queryer = (pgx.Tx)(nil)
