// Package consumer applies movement events from the broker under one of four
// delivery configurations.
//
// The four modes differ ONLY in the order of three operations -- poll, apply,
// commit -- and in whether the apply is deduplicated. Writing them as one loop
// with a switch, rather than four implementations, is deliberate: Finding 2
// compares them, and four separate loops would differ in incidental ways that
// contaminate the comparison.
//
// Mode C is written first and the others are expressed as documented
// weakenings of it, because C is the only one that is correct.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	shadowbookv1 "github.com/roshanrana/shadowbook/gen/go/shadowbook/v1"
	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/broker"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

// Mode is a delivery configuration. The letters are the ablation row labels.
type Mode string

const (
	// AtMostOnce commits offsets BEFORE applying. A crash between the two
	// loses the batch. Expected: lost > 0, duplicated = 0.
	AtMostOnce Mode = "A"
	// AtLeastOnce applies then commits, with no deduplication. Redelivery
	// re-applies. Expected: lost = 0, duplicated > 0.
	AtLeastOnce Mode = "B"
	// InboxDedup applies and records the message id in ONE transaction, so a
	// redelivery hits a unique constraint. Expected: lost = 0, duplicated = 0,
	// at a latency cost. This is the correct design.
	InboxDedup Mode = "C"
	// Transactional additionally commits offsets inside a Kafka transaction.
	// Expected: as C, with the guarantee extended to the broker -- but it
	// still ends at the database boundary.
	Transactional Mode = "D"
)

// Valid reports whether m is one of the four.
func (m Mode) Valid() bool {
	switch m {
	case AtMostOnce, AtLeastOnce, InboxDedup, Transactional:
		return true
	}
	return false
}

// ErrUnknownMode is returned for an unrecognised configuration.
var ErrUnknownMode = errors.New("consumer: unknown delivery mode")

// SuspenseAccountFor names the contra account a one-sided legacy movement is
// booked against.
//
// A movement carries one account and one amount; double entry needs at least
// two legs summing to zero (FR-L2). The shadow therefore books every movement
// against a per-currency suspense account. This is a modelling decision, not a
// fudge: it keeps the global invariant true by construction, and the suspense
// balance is itself meaningful -- it is the net of everything the legacy core
// has sent that has no counterparty in the shadow.
func SuspenseAccountFor(cur money.Currency) uuid.UUID {
	return uuid.NewSHA1(suspenseNamespace, []byte("suspense/"+string(cur)))
}

var suspenseNamespace = uuid.MustParse("9c4e5b18-27f3-5a6d-b0e4-1d7f8c3a5b92")

// Stats are one run's counters, the raw material of Finding 2.
type Stats struct {
	Polled     int
	Applied    int
	Duplicates int // suppressed by the inbox constraint (mode C and D only)
	Failed     int
}

// Options configure a Consumer.
type Options struct {
	Mode    Mode
	Topic   string
	Batch   int
	Metrics *obs.Metrics
}

// Consumer applies movements.
type Consumer struct {
	st   *store.Store
	br   broker.Consumer
	mode Mode
	opts Options
}

// New builds a consumer.
func New(st *store.Store, br broker.Consumer, o Options) (*Consumer, error) {
	if !o.Mode.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownMode, o.Mode)
	}
	if o.Topic == "" {
		o.Topic = "shadowbook.movements.v1"
	}
	if o.Batch <= 0 {
		o.Batch = 100
	}
	return &Consumer{st: st, br: br, mode: o.Mode, opts: o}, nil
}

// RunOnce polls and processes a single batch.
func (c *Consumer) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats

	records, err := c.br.Poll(ctx, c.opts.Batch)
	if err != nil {
		return st, fmt.Errorf("consumer: poll: %w", err)
	}
	st.Polled = len(records)
	if len(records) == 0 {
		return st, nil
	}

	// Mode A commits BEFORE applying. Everything between here and the apply is
	// a window in which a crash loses the batch -- that is the whole point of
	// configuration A, and it is why it is first in the table.
	if c.mode == AtMostOnce {
		if err := c.br.Commit(ctx, records); err != nil {
			return st, fmt.Errorf("consumer: commit (mode A): %w", err)
		}
	}

	for _, rec := range records {
		applied, err := c.apply(ctx, rec)
		switch {
		case err != nil:
			st.Failed++
			c.count("failed")
			return st, err
		case applied:
			st.Applied++
			c.count("applied")
		default:
			st.Duplicates++
			c.count("duplicate_suppressed")
		}
	}

	// B, C and D commit after applying. For D this is where the Kafka
	// transaction would be committed alongside the offsets; with the franz-go
	// GroupTransactSession the produce, the apply and the offset commit are one
	// unit. See the note on Transactional above for where the guarantee ends.
	if c.mode != AtMostOnce {
		if err := c.br.Commit(ctx, records); err != nil {
			return st, fmt.Errorf("consumer: commit (mode %s): %w", c.mode, err)
		}
	}

	if lag, err := c.br.Lag(ctx); err == nil && c.opts.Metrics != nil {
		c.opts.Metrics.ConsumerLag.WithLabelValues(string(c.mode)).Set(float64(lag))
	}
	return st, nil
}

func (c *Consumer) count(result string) {
	if c.opts.Metrics != nil {
		c.opts.Metrics.MovementsTotal.WithLabelValues(string(c.mode), result).Inc()
	}
}

// apply books one movement. Returns false when the message was a duplicate that
// the inbox constraint suppressed.
func (c *Consumer) apply(ctx context.Context, rec broker.Record) (bool, error) {
	var ev shadowbookv1.MovementEvent
	if err := proto.Unmarshal(rec.Value, &ev); err != nil {
		return false, fmt.Errorf("consumer: unmarshal movement: %w", err)
	}
	if ev.GetMessageId() == "" {
		return false, errors.New("consumer: movement has no message_id")
	}

	accountID, err := uuid.Parse(ev.GetAccountId())
	if err != nil {
		return false, fmt.Errorf("consumer: account id: %w", err)
	}
	cur := money.Currency(ev.GetAmount().GetCurrency())
	amt, err := money.New(ev.GetAmount().GetMinor(), cur)
	if err != nil {
		return false, err
	}
	businessDate, err := bizdate.Parse(ev.GetBusinessDate())
	if err != nil {
		return false, fmt.Errorf("consumer: business_date: %w", err)
	}
	valueDate, err := bizdate.Parse(ev.GetValueDate())
	if err != nil {
		return false, fmt.Errorf("consumer: value_date: %w", err)
	}
	postedAt, err := time.Parse(time.RFC3339Nano, ev.GetPostedAt())
	if err != nil {
		return false, fmt.Errorf("consumer: posted_at: %w", err)
	}

	tx, err := c.st.Begin(ctx)
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	// Modes C and D claim the message id in the SAME transaction as the effect.
	// A redelivery raises 23505 on inbox_pkey and the whole transaction rolls
	// back, so the effect happens exactly once. Modes A and B skip this, which
	// is precisely why B duplicates.
	if c.mode == InboxDedup || c.mode == Transactional {
		err := store.ClaimInboxMessage(ctx, tx, ev.GetMessageId(), rec.Topic, rec.Partition, rec.Offset)
		if err != nil {
			if store.IsUniqueViolation(err, store.ConstraintInbox) {
				return false, nil // already applied; not an error
			}
			return false, fmt.Errorf("consumer: claim inbox: %w", err)
		}
	}

	// A movement id is stable, so the posting id derived from it is stable too
	// (NFR-5). Mode B deliberately gets a fresh id per delivery -- that is how
	// its duplicates become visible as separate postings rather than collapsing
	// into one by accident.
	postingID := uuid.NewSHA1(suspenseNamespace, []byte("movement/"+ev.GetMessageId()))
	if c.mode == AtLeastOnce || c.mode == AtMostOnce {
		postingID = uuid.New()
	}

	if err := store.InsertPosting(ctx, tx, store.Posting{
		ID: postingID, Principal: "consumer", Kind: kindOrTransfer(ev.GetKind()),
		Currency: cur, BusinessDate: businessDate, ValueDate: valueDate, PostedAt: postedAt.UTC(),
	}); err != nil {
		return false, fmt.Errorf("consumer: insert posting: %w", err)
	}

	suspense := SuspenseAccountFor(cur)
	if _, err := store.InsertEntries(ctx, tx, []store.Entry{
		{PostingID: postingID, AccountID: accountID, Amount: amt,
			BusinessDate: businessDate, ValueDate: valueDate},
		{PostingID: postingID, AccountID: suspense, Amount: amt.Neg(),
			BusinessDate: businessDate, ValueDate: valueDate},
	}); err != nil {
		return false, fmt.Errorf("consumer: insert entries: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("consumer: commit: %w", err)
	}
	committed = true
	return true, nil
}

func kindOrTransfer(k string) string {
	switch k {
	case "transfer", "interest", "fee", "reversal":
		return k
	default:
		return "transfer"
	}
}

// Run consumes until ctx is cancelled, then returns after the in-flight batch.
// No goroutine is started without a cancellation path (CLAUDE.md).
func (c *Consumer) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		st, err := c.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, broker.ErrClosed) {
				return nil
			}
			return err
		}
		if st.Polled == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
}

// EnsureSuspenseAccounts creates the per-currency contra accounts. Called at
// start-up; safe to call repeatedly.
func EnsureSuspenseAccounts(ctx context.Context, st *store.Store, opened bizdate.BusinessDate) error {
	for _, cur := range []money.Currency{"USD", "EUR", "JPY"} {
		err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: SuspenseAccountFor(cur), ProductCode: "SUSPENSE", Currency: cur, OpenedOn: opened,
		})
		if err != nil && !store.IsUniqueViolation(err, "") {
			return fmt.Errorf("consumer: suspense account %s: %w", cur, err)
		}
	}
	return nil
}
