package chaos

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeDocker records what it was asked to do, in order.
type fakeDocker struct {
	calls []string
	fail  map[string]error
}

func (f *fakeDocker) Kill(_ context.Context, c string) error {
	f.calls = append(f.calls, "kill:"+c)
	return f.fail["kill:"+c]
}

func (f *fakeDocker) Start(_ context.Context, c string) error {
	f.calls = append(f.calls, "start:"+c)
	return f.fail["start:"+c]
}

// A virtual clock, so a four-minute schedule is tested in microseconds and is
// not at the mercy of scheduler jitter.
func virtualRunner(d Docker) (*Runner, *time.Duration) {
	elapsed := new(time.Duration)
	base := time.Date(2028, time.February, 29, 12, 0, 0, 0, time.UTC)
	r := &Runner{
		Docker: d,
		Now:    func() time.Time { return base.Add(*elapsed) },
		Sleep: func(_ context.Context, d time.Duration) error {
			*elapsed += d
			return nil
		},
	}
	return r, elapsed
}

func TestDefaultScheduleIsValid(t *testing.T) {
	if err := Validate(DefaultSchedule()); err != nil {
		t.Fatalf("the shipped default schedule is invalid: %v", err)
	}
}

func TestRunExecutesInOrderAtTheScheduledTimes(t *testing.T) {
	f := &fakeDocker{}
	r, _ := virtualRunner(f)

	records, err := r.Run(context.Background(), DefaultSchedule())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kill:redpanda-1", "start:redpanda-1", "kill:redpanda-2", "start:redpanda-2"}
	if len(f.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, f.calls[i], want[i])
		}
	}
	for i, rec := range records {
		if rec.Executed != rec.Event.At {
			t.Fatalf("record %d executed at %v, scheduled %v", i, rec.Executed, rec.Event.At)
		}
		if rec.Err != "" {
			t.Fatalf("record %d errored: %s", i, rec.Err)
		}
	}
}

func TestOutOfOrderScheduleIsSortedNotRejected(t *testing.T) {
	f := &fakeDocker{}
	r, _ := virtualRunner(f)
	shuffled := []Event{
		{At: 90 * time.Second, Action: Start, Broker: "b1"},
		{At: 60 * time.Second, Action: Kill, Broker: "b1"},
	}
	if _, err := r.Run(context.Background(), shuffled); err != nil {
		t.Fatal(err)
	}
	if f.calls[0] != "kill:b1" {
		t.Fatalf("schedule was not sorted: %v", f.calls)
	}
}

// A failed kill is a fact the artefact must carry, not a reason to abandon the
// run: the ledger's behaviour under a partly-broken cluster is the measurement.
func TestAFailedActionIsRecordedAndTheRunContinues(t *testing.T) {
	f := &fakeDocker{fail: map[string]error{"kill:redpanda-1": errors.New("no such container")}}
	r, _ := virtualRunner(f)

	records, err := r.Run(context.Background(), DefaultSchedule())
	if err != nil {
		t.Fatalf("run aborted: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("got %d records, want all 4", len(records))
	}
	if records[0].Err == "" {
		t.Fatal("the failure was not recorded")
	}
	if records[3].Err != "" {
		t.Fatal("a later action was skipped after an earlier failure")
	}
}

func TestCancellationStopsTheSchedule(t *testing.T) {
	f := &fakeDocker{}
	r := NewRunner(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Run(ctx, DefaultSchedule()); err == nil {
		t.Fatal("a cancelled context did not stop the schedule")
	}
	if len(f.calls) != 0 {
		t.Fatalf("actions ran after cancellation: %v", f.calls)
	}
}

// Two brokers down at once loses quorum on RF=3 with min.insync.replicas=2.
// That stops being a delivery-semantics experiment and becomes an availability
// one, so the schedule is rejected rather than silently changing the finding.
func TestValidateRejectsSchedulesThatWouldLoseQuorum(t *testing.T) {
	for _, tc := range []struct {
		name     string
		schedule []Event
		want     string
	}{
		{
			name: "two brokers down at once",
			schedule: []Event{
				{At: 10 * time.Second, Action: Kill, Broker: "b1"},
				{At: 20 * time.Second, Action: Kill, Broker: "b2"},
				{At: 30 * time.Second, Action: Start, Broker: "b1"},
				{At: 40 * time.Second, Action: Start, Broker: "b2"},
			},
			want: "quorum",
		},
		{
			name: "killed twice",
			schedule: []Event{
				{At: 10 * time.Second, Action: Kill, Broker: "b1"},
				{At: 20 * time.Second, Action: Kill, Broker: "b1"},
			},
			want: "killed twice",
		},
		{
			name:     "started without being killed",
			schedule: []Event{{At: 10 * time.Second, Action: Start, Broker: "b1"}},
			want:     "without being killed",
		},
		{
			name:     "never restarted",
			schedule: []Event{{At: 10 * time.Second, Action: Kill, Broker: "b1"}},
			want:     "never restarted",
		},
		{
			name:     "empty",
			schedule: nil,
			want:     "empty",
		},
		{
			name:     "unknown action",
			schedule: []Event{{At: time.Second, Action: "reboot", Broker: "b1"}},
			want:     "unknown action",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.schedule)
			if err == nil {
				t.Fatalf("accepted an invalid schedule")
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
