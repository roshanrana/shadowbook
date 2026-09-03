// Package outbox relays committed posting events to the broker.
//
// At-least-once by construction: rows are marked sent only after every produce
// in the batch is acknowledged, so a crash between produce and mark replays the
// batch rather than losing it. Per-account ordering holds because the poll is
// ordered by outbox_id and the partition key is the account.
package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/roshanrana/shadowbook/internal/broker"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

// DefaultBatchSize is the LLD's batch bound.
const DefaultBatchSize = 500

// Relay drains the outbox to a topic.
type Relay struct {
	st        *store.Store
	prod      broker.Producer
	topic     string
	batchSize int
	interval  time.Duration
}

// Options configure the relay.
type Options struct {
	Topic     string
	BatchSize int
	Interval  time.Duration
}

// New builds a relay.
func New(st *store.Store, prod broker.Producer, o Options) *Relay {
	if o.Topic == "" {
		o.Topic = "shadowbook.postings.v1"
	}
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.Interval <= 0 {
		o.Interval = 50 * time.Millisecond
	}
	return &Relay{st: st, prod: prod, topic: o.Topic, batchSize: o.BatchSize, interval: o.Interval}
}

// Drain moves one batch and reports how many rows were sent.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	rows, err := store.ClaimOutboxBatch(ctx, r.st.Pool, r.batchSize)
	if err != nil {
		return 0, fmt.Errorf("outbox: claim: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	records := make([]broker.Record, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		records = append(records, broker.Record{
			Topic: r.topic,
			Key:   row.PartitionKey, // account id: this is what preserves per-account order
			Value: row.Payload,
		})
		ids = append(ids, row.ID)
	}

	// Produce first. If this fails the rows stay unsent and the batch is
	// retried -- at-least-once, never at-most-once.
	if err := r.prod.Produce(ctx, records); err != nil {
		return 0, fmt.Errorf("outbox: produce: %w", err)
	}
	if err := store.MarkOutboxSent(ctx, r.st.Pool, ids); err != nil {
		// Produced but not marked: the batch will be produced again on the next
		// pass. Duplicates downstream are the consumer's problem to dedupe,
		// which is exactly what the inbox exists for.
		return len(rows), fmt.Errorf("outbox: mark sent: %w", err)
	}
	return len(rows), nil
}

// Run drains until ctx is cancelled, then drains once more so shutdown does not
// strand a committed posting (FR-L10).
func (r *Relay) Run(ctx context.Context) error {
	t := time.NewTicker(r.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final drain on a short independent deadline: ctx is already done.
			drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			for {
				n, err := r.Drain(drainCtx)
				if err != nil || n == 0 {
					return nil
				}
			}
		case <-t.C:
			for {
				n, err := r.Drain(ctx)
				if err != nil {
					return err
				}
				if n < r.batchSize {
					break // caught up
				}
			}
		}
	}
}
