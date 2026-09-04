package ablation

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roshanrana/shadowbook/internal/harness/chaos"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
)

func base(config consumer.Mode, runID string) Artefact {
	return Artefact{
		RunID: runID, Config: config,
		Seed: 20260903, Profile: "payday", RatePerSec: 1000, DurationSec: 240,
		Schedule: chaos.DefaultSchedule(), LedgerSHA: "abc123", BrokerVersion: "v24.3.6",
		Sent: 240_000, Applied: 240_000, InvariantHeld: true, Drained: true,
		P50: 1000, P95: 9000, P99: 40_000, LagPeak: 120, DrainSeconds: 3.5,
	}
}

func threeRuns(config consumer.Mode, mutate func(int, *Artefact)) []Artefact {
	out := make([]Artefact, 0, 3)
	for i := 0; i < 3; i++ {
		a := base(config, string(config)+"-"+string(rune('a'+i)))
		if mutate != nil {
			mutate(i, &a)
		}
		out = append(out, a)
	}
	return out
}

func TestTableFoldsThreeRunsIntoOneRow(t *testing.T) {
	runs := threeRuns(consumer.InboxDedup, func(i int, a *Artefact) {
		a.P99 = int64(40_000 + i*1_000)
	})
	rows, err := Table(runs, MinRuns)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Config != "C" || rows[0].Runs != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	// Median with the min-max range, per the findings-report skill.
	if !strings.Contains(rows[0].P99, "[") {
		t.Fatalf("p99 = %q; a varying measurement must show its range", rows[0].P99)
	}
	if !strings.HasPrefix(rows[0].P99, "41ms") {
		t.Fatalf("p99 median = %q, want the middle run (41ms)", rows[0].P99)
	}
	// An identical measurement across runs prints as one number, not a range.
	if strings.Contains(rows[0].Sent, "[") {
		t.Fatalf("sent = %q; identical values should not print a range", rows[0].Sent)
	}
}

// The guard the whole finding rests on: rows must come from runs whose fixed
// parameters agree, or the table compares configurations to each other AND to a
// changed experiment at the same time.
func TestTableRefusesMismatchedFixedParameters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Artefact)
	}{
		{"seed", func(a *Artefact) { a.Seed = 1 }},
		{"profile", func(a *Artefact) { a.Profile = "steady" }},
		{"rate", func(a *Artefact) { a.RatePerSec = 500 }},
		{"duration", func(a *Artefact) { a.DurationSec = 120 }},
		{"ledger sha", func(a *Artefact) { a.LedgerSHA = "deadbeef" }},
		{"broker version", func(a *Artefact) { a.BrokerVersion = "v24.2.0" }},
		{"schedule", func(a *Artefact) {
			a.Schedule = append(a.Schedule, chaos.Event{At: time.Minute, Action: chaos.Kill, Broker: "x"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := threeRuns(consumer.InboxDedup, nil)
			tc.mutate(&runs[2])
			_, err := Table(runs, MinRuns)
			if err == nil {
				t.Fatalf("a table was rendered from runs with a differing %s", tc.name)
			}
			var mismatch *ErrMismatchedParameters
			if !errors.As(err, &mismatch) {
				t.Fatalf("error type = %T, want *ErrMismatchedParameters", err)
			}
			if !strings.Contains(err.Error(), "refusing") {
				t.Fatalf("error = %q; it should say it is refusing and why", err)
			}
		})
	}
}

func TestTableRefusesTooFewRuns(t *testing.T) {
	runs := threeRuns(consumer.InboxDedup, nil)[:2]
	_, err := Table(runs, MinRuns)
	if err == nil {
		t.Fatal("two runs of a chaotic system were accepted as a result")
	}
	if !strings.Contains(err.Error(), "need at least 3") {
		t.Fatalf("error = %q", err)
	}
}

func TestTableRefusesNoArtefacts(t *testing.T) {
	if _, err := Table(nil, MinRuns); err == nil {
		t.Fatal("an empty artefact set produced a table")
	}
}

func TestConfigurationsAreOrderedAToD(t *testing.T) {
	var runs []Artefact
	for _, c := range []consumer.Mode{consumer.Transactional, consumer.AtMostOnce, consumer.InboxDedup, consumer.AtLeastOnce} {
		runs = append(runs, threeRuns(c, nil)...)
	}
	rows, err := Table(runs, MinRuns)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Config)
	}
	if strings.Join(got, "") != "ABCD" {
		t.Fatalf("config order = %v, want A B C D", got)
	}
}

func TestInvariantHeldIsFalseIfAnyRunBrokeIt(t *testing.T) {
	runs := threeRuns(consumer.AtLeastOnce, func(i int, a *Artefact) {
		if i == 1 {
			a.InvariantHeld = false
		}
	})
	rows, err := Table(runs, MinRuns)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].InvariantHeld {
		t.Fatal("one run broke the invariant and the row still says it held")
	}
}

func TestWriteAndLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	for _, c := range []consumer.Mode{consumer.AtMostOnce, consumer.InboxDedup} {
		for _, a := range threeRuns(c, nil) {
			if _, err := a.Write(root); err != nil {
				t.Fatal(err)
			}
		}
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 6 {
		t.Fatalf("loaded %d artefacts, want 6", len(loaded))
	}
	if loaded[0].Config != consumer.AtMostOnce {
		t.Fatalf("artefacts are not sorted by config: %v", loaded[0].Config)
	}
	if _, err := Table(loaded, MinRuns); err != nil {
		t.Fatalf("round-tripped artefacts would not render: %v", err)
	}
	if _, err := Load(filepath.Join(root, "nope")); err == nil {
		t.Fatal("loading a missing directory should fail")
	}
}

func TestTableRefusesFakeBrokerArtefacts(t *testing.T) {
	// Deliberately self-consistent: every fixed parameter agrees, so the
	// mismatch guard would pass these. Only the broker marks them as a smoke
	// run, and that must be enough to stop them.
	var arts []Artefact
	for _, mode := range []consumer.Mode{consumer.AtMostOnce, consumer.AtLeastOnce, consumer.InboxDedup} {
		for i := 0; i < MinRuns; i++ {
			arts = append(arts, Artefact{
				RunID: fmt.Sprintf("smoke-%s-%d", mode, i), Config: mode,
				Seed: 7, Profile: "steady", RatePerSec: 100, DurationSec: 10,
				LedgerSHA: "abc", BrokerVersion: BrokerFake + "kfake",
				Sent: 1000, Applied: 1000, InvariantHeld: true, Drained: true,
			})
		}
	}
	_, err := Table(arts, MinRuns)
	if err == nil {
		t.Fatal("Table rendered a finding from in-process-broker runs")
	}
	var notMeasurement *ErrNotAMeasurement
	if !errors.As(err, &notMeasurement) {
		t.Fatalf("err = %v, want ErrNotAMeasurement", err)
	}
	if !strings.Contains(err.Error(), "make up-chaos") {
		t.Fatalf("error does not say how to get a real measurement: %v", err)
	}
}

func TestTableAcceptsRealBrokerArtefacts(t *testing.T) {
	// The mirror image, so the guard above is not passing for some unrelated
	// reason: identical artefacts with a real broker version must render.
	var arts []Artefact
	for _, mode := range []consumer.Mode{consumer.AtMostOnce, consumer.AtLeastOnce, consumer.InboxDedup} {
		for i := 0; i < MinRuns; i++ {
			arts = append(arts, Artefact{
				RunID: fmt.Sprintf("real-%s-%d", mode, i), Config: mode,
				Seed: 7, Profile: "steady", RatePerSec: 100, DurationSec: 10,
				LedgerSHA: "abc", BrokerVersion: "redpanda v24.3.6",
				Sent: 1000, Applied: 1000, InvariantHeld: true, Drained: true,
			})
		}
	}
	rows, err := Table(arts, MinRuns)
	if err != nil {
		t.Fatalf("Table refused real runs: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
}

func TestTableRefusesRunsThatNeverDrained(t *testing.T) {
	// A run cut off mid-drain reports a huge Lost column made of records that
	// were sitting intact on the broker. The first real-cluster sweep produced
	// exactly that -- 122,886 "lost" records that had never been consumed --
	// and every fixed parameter agreed, so nothing else would have stopped it.
	var arts []Artefact
	for _, mode := range []consumer.Mode{consumer.AtMostOnce, consumer.AtLeastOnce, consumer.InboxDedup} {
		for i := 0; i < MinRuns; i++ {
			a := base(mode, fmt.Sprintf("undrained-%s-%d", mode, i))
			a.Drained = false
			arts = append(arts, a)
		}
	}
	_, err := Table(arts, MinRuns)
	if err == nil {
		t.Fatal("Table rendered a finding from runs that never drained")
	}
	if !strings.Contains(err.Error(), "never drained") {
		t.Fatalf("error does not name the cause: %v", err)
	}
}

func TestPercentileByNearestRank(t *testing.T) {
	// Nearest-rank, so p50 of ten values is the fifth, not an interpolation.
	// Latency percentiles that interpolate between two observed values report
	// a number nobody measured.
	s := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, tc := range []struct {
		p    int
		want int64
	}{
		{50, 5}, {95, 10}, {99, 10}, {1, 1}, {100, 10},
	} {
		if got := percentile(s, tc.p); got != tc.want {
			t.Fatalf("p%d = %d, want %d", tc.p, got, tc.want)
		}
	}
	if got := percentile(nil, 50); got != 0 {
		t.Fatalf("percentile of nothing = %d, want 0", got)
	}
	if got := percentile([]int64{42}, 99); got != 42 {
		t.Fatalf("single value p99 = %d, want 42", got)
	}
}

func TestSequenceOfMessageID(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want int64
		ok   bool
	}{
		{"mv-20260904-0", 0, true},
		{"mv-20260904-35999", 35999, true},
		{"mv-20260904-", 0, false},
		{"nodashes", 0, false},
		{"mv-20260904-abc", 0, false},
	} {
		got, ok := sequenceOf(tc.id)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("sequenceOf(%q) = %d,%v; want %d,%v", tc.id, got, ok, tc.want, tc.ok)
		}
	}
}
