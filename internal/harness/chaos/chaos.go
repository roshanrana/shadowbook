// Package chaos executes the scripted broker kill schedule of FR-H2.
//
// The schedule is DATA, not code, and it is recorded verbatim in every run
// artefact. Finding 2 compares configurations under identical chaos; a schedule
// that drifted between runs -- because it was expressed as sleeps scattered
// through a script -- would invalidate the comparison without anything failing.
package chaos

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Action is what to do to a broker.
type Action string

const (
	Kill  Action = "kill"
	Start Action = "start"
)

// Event is one scheduled action.
type Event struct {
	At     time.Duration `json:"at"`
	Action Action        `json:"action"`
	Broker string        `json:"broker"`
}

// DefaultSchedule is the schedule from the chaos-ablation skill. Two brokers
// are killed and restarted while load runs, so the cluster loses a replica
// twice without ever losing quorum.
func DefaultSchedule() []Event {
	return []Event{
		{At: 60 * time.Second, Action: Kill, Broker: "redpanda-1"},
		{At: 90 * time.Second, Action: Start, Broker: "redpanda-1"},
		{At: 150 * time.Second, Action: Kill, Broker: "redpanda-2"},
		{At: 180 * time.Second, Action: Start, Broker: "redpanda-2"},
	}
}

// Docker is the container control surface. An interface so the schedule can be
// tested exhaustively without a daemon.
type Docker interface {
	Kill(ctx context.Context, container string) error
	Start(ctx context.Context, container string) error
}

// Record is one executed action, kept so the artefact can state the schedule AS
// EXECUTED rather than as intended.
type Record struct {
	Event    Event         `json:"event"`
	Executed time.Duration `json:"executed_at"`
	Err      string        `json:"error,omitempty"`
}

// Runner executes a schedule against a Docker implementation.
type Runner struct {
	Docker Docker
	// Now and Sleep are injected so tests run in microseconds instead of
	// minutes, and so a run is not at the mercy of scheduler jitter.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewRunner builds a runner with real time.
func NewRunner(d Docker) *Runner {
	return &Runner{
		Docker: d,
		Now:    time.Now,
		Sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

// Run executes the schedule in order and returns what actually happened.
//
// A failed action does not abort the run: the point is to observe the ledger
// under a partly-broken cluster, and a kill that fails is itself a fact the
// artefact should carry.
func (r *Runner) Run(ctx context.Context, schedule []Event) ([]Record, error) {
	ordered := append([]Event(nil), schedule...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At < ordered[j].At })

	start := r.Now()
	out := make([]Record, 0, len(ordered))

	for _, ev := range ordered {
		wait := ev.At - r.Now().Sub(start)
		if wait > 0 {
			if err := r.Sleep(ctx, wait); err != nil {
				return out, fmt.Errorf("chaos: interrupted before %s %s: %w", ev.Action, ev.Broker, err)
			}
		}
		rec := Record{Event: ev, Executed: r.Now().Sub(start)}

		var err error
		switch ev.Action {
		case Kill:
			err = r.Docker.Kill(ctx, ev.Broker)
		case Start:
			err = r.Docker.Start(ctx, ev.Broker)
		default:
			err = fmt.Errorf("unknown action %q", ev.Action)
		}
		if err != nil {
			rec.Err = err.Error()
		}
		out = append(out, rec)
	}
	return out, nil
}

// Validate rejects a schedule that could not produce a meaningful run.
func Validate(schedule []Event) error {
	if len(schedule) == 0 {
		return fmt.Errorf("chaos: empty schedule")
	}
	down := map[string]bool{}
	for _, ev := range schedule {
		switch ev.Action {
		case Kill:
			if down[ev.Broker] {
				return fmt.Errorf("chaos: %s killed twice without a restart", ev.Broker)
			}
			down[ev.Broker] = true
		case Start:
			if !down[ev.Broker] {
				return fmt.Errorf("chaos: %s started without being killed", ev.Broker)
			}
			down[ev.Broker] = false
		default:
			return fmt.Errorf("chaos: unknown action %q", ev.Action)
		}
		// Two brokers down at once loses quorum on RF=3 with
		// min.insync.replicas=2, which stops being a delivery-semantics
		// experiment and becomes an availability one.
		downCount := 0
		for _, d := range down {
			if d {
				downCount++
			}
		}
		if downCount > 1 {
			return fmt.Errorf("chaos: schedule takes %d brokers down at once; quorum would be lost", downCount)
		}
	}
	for broker, still := range down {
		if still {
			return fmt.Errorf("chaos: %s is never restarted", broker)
		}
	}
	return nil
}
