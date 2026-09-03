// Package broker is the seam between the ledger and Kafka/Redpanda.
//
// Producer and Consumer are interfaces so the outbox relay and the movement
// consumer can be tested exhaustively without a broker -- including the
// failure modes Finding 2 is about, which are far easier to provoke in a fake
// than by killing a container. The franz-go implementations live alongside and
// are exercised against real Redpanda in the ablation runs.
package broker

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// Record is one message.
type Record struct {
	Topic     string
	Key       string
	Value     []byte
	Partition int32
	Offset    int64
}

// Producer writes records. Produce must not return until every record is
// durably acknowledged by the broker (acks=all, NFR-9), because the outbox
// relay marks rows sent immediately afterwards.
type Producer interface {
	Produce(ctx context.Context, records []Record) error
	Close() error
}

// Consumer reads records and commits offsets. The four delivery modes differ
// only in the ORDER in which Poll, apply and Commit are called, which is what
// makes them comparable under identical chaos.
type Consumer interface {
	Poll(ctx context.Context, max int) ([]Record, error)
	Commit(ctx context.Context, records []Record) error
	Lag(ctx context.Context) (int64, error)
	Close() error
}

// ErrClosed is returned by a closed fake.
var ErrClosed = errors.New("broker: closed")

// Fake is an in-memory broker for tests. It is deliberately able to lie: it can
// fail produces and replay records, which is how the consumer modes' loss and
// duplication properties are demonstrated without killing a container.
type Fake struct {
	mu     sync.Mutex
	log    map[string][]Record // topic -> records in order
	cursor map[string]int      // topic -> next unread index
	closed bool

	// FailProduceAfter makes the next produce fail once N records have been
	// accepted, standing in for a broker dying mid-batch.
	FailProduceAfter int
	produced         int

	// RedeliverFrom, when non-negative, rewinds the cursor on the next Poll,
	// standing in for an uncommitted offset after a restart.
	RedeliverFrom int
}

// NewFake builds an empty in-memory broker.
func NewFake() *Fake {
	return &Fake{log: map[string][]Record{}, cursor: map[string]int{}, RedeliverFrom: -1}
}

// Produce appends records, honouring FailProduceAfter.
func (f *Fake) Produce(ctx context.Context, records []Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, r := range records {
		if f.FailProduceAfter > 0 && f.produced >= f.FailProduceAfter {
			return errors.New("broker: simulated produce failure")
		}
		r.Partition = 0
		r.Offset = int64(len(f.log[r.Topic]))
		f.log[r.Topic] = append(f.log[r.Topic], r)
		f.produced++
	}
	return nil
}

// Poll returns up to max unread records from every topic, topic order sorted so
// the fake is deterministic (NFR-5).
func (f *Fake) Poll(ctx context.Context, max int) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.RedeliverFrom >= 0 {
		for t := range f.cursor {
			f.cursor[t] = f.RedeliverFrom
		}
		f.RedeliverFrom = -1
	}
	topics := make([]string, 0, len(f.log))
	for t := range f.log {
		topics = append(topics, t)
	}
	sort.Strings(topics)

	var out []Record
	for _, t := range topics {
		for f.cursor[t] < len(f.log[t]) && len(out) < max {
			out = append(out, f.log[t][f.cursor[t]])
			f.cursor[t]++
		}
	}
	return out, nil
}

// Commit is a no-op for the fake: the cursor already advanced in Poll, and the
// modes' differences are expressed by when they call Commit relative to
// applying, not by what Commit does.
func (f *Fake) Commit(context.Context, []Record) error { return nil }

// Rewind puts the cursor back, standing in for redelivery after a crash.
func (f *Fake) Rewind(topic string, to int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursor[topic] = to
}

// Lag is the unread count across topics.
func (f *Fake) Lag(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lag int64
	for t, rs := range f.log {
		lag += int64(len(rs) - f.cursor[t])
	}
	return lag, nil
}

// Records returns everything produced to a topic, for assertions.
func (f *Fake) Records(topic string) []Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Record, len(f.log[topic]))
	copy(out, f.log[topic])
	return out
}

// Close marks the fake closed.
func (f *Fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

var (
	_ Producer = (*Fake)(nil)
	_ Consumer = (*Fake)(nil)
)
