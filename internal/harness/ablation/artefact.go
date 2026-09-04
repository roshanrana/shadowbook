// Package ablation runs the delivery-semantics comparison and writes the run
// artefacts Finding 2 is rendered from.
//
// The runner REFUSES to render a table from artefacts whose fixed parameters
// differ. Seed, profile, rate, schedule, ledger SHA and broker version must be
// identical across every row, or the table is comparing configurations to each
// other AND to a changed experiment at the same time. That guard is the whole
// reason the finding means anything (chaos-ablation skill, D-007).
package ablation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roshanrana/shadowbook/internal/harness/chaos"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
)

// Artefact is one run of one configuration. It is the only input to
// `make report`, which never touches a live system.
type Artefact struct {
	RunID  string        `json:"run_id"`
	Config consumer.Mode `json:"config"`

	// --- fixed parameters: these MUST agree across every artefact in a table
	Seed          int64          `json:"seed"`
	Profile       string         `json:"profile"`
	RatePerSec    int            `json:"rate_per_sec"`
	DurationSec   int            `json:"duration_sec"`
	Schedule      []chaos.Event  `json:"schedule"`
	LedgerSHA     string         `json:"ledger_sha"`
	BrokerVersion string         `json:"broker_version"`
	Executed      []chaos.Record `json:"executed_schedule"`

	// --- measurements
	Sent          int64   `json:"sent"`
	Applied       int64   `json:"applied"`
	Lost          int64   `json:"lost"`
	Duplicated    int64   `json:"duplicated"`
	InFlight      int64   `json:"in_flight_at_drain"`
	P50           int64   `json:"p50_micros"`
	P95           int64   `json:"p95_micros"`
	P99           int64   `json:"p99_micros"`
	LagPeak       int64   `json:"lag_peak"`
	DrainSeconds  float64 `json:"drain_seconds"`
	InvariantHeld bool    `json:"invariant_held"`
}

// fixedKey is everything that must match across a table.
func (a Artefact) fixedKey() string {
	sched, _ := json.Marshal(a.Schedule)
	return fmt.Sprintf("%d|%s|%d|%d|%s|%s|%s",
		a.Seed, a.Profile, a.RatePerSec, a.DurationSec, a.LedgerSHA, a.BrokerVersion, sched)
}

// Write persists an artefact under reports/runs/<run_id>/artefact.json.
func (a Artefact) Write(root string) (string, error) {
	dir := filepath.Join(root, a.RunID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("ablation: mkdir: %w", err)
	}
	path := filepath.Join(dir, "artefact.json")
	blob, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return "", fmt.Errorf("ablation: marshal: %w", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("ablation: write: %w", err)
	}
	return path, nil
}

// Load reads every artefact under root.
func Load(root string) ([]Artefact, error) {
	var out []Artefact
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Base(path) != "artefact.json" {
			return nil
		}
		blob, err := os.ReadFile(path) //nolint:gosec // path comes from the walk
		if err != nil {
			return err
		}
		var a Artefact
		if err := json.Unmarshal(blob, &a); err != nil {
			return fmt.Errorf("ablation: %s: %w", path, err)
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Config != out[j].Config {
			return out[i].Config < out[j].Config
		}
		return out[i].RunID < out[j].RunID
	})
	return out, nil
}

// ErrMismatchedParameters is returned when artefacts disagree on a fixed
// parameter. It names the field, because "the runs differ" is not actionable.
type ErrMismatchedParameters struct {
	Detail string
}

func (e *ErrMismatchedParameters) Error() string {
	return "ablation: refusing to render a table from mismatched runs: " + e.Detail
}

// BrokerFake is the BrokerVersion recorded by a run against an in-process
// broker rather than a real cluster.
//
// Such runs are useful and are meant to exist: they exercise the orchestration
// -- provisioning, load, draining, measurement, artefact writing -- on a
// machine with no Docker. What they cannot do is produce Finding 2, which is a
// measurement of a real cluster losing replicas under load (HLD, "the broker
// hop must be real"). An in-process broker cannot be killed mid-write, so its
// loss and duplication columns describe the harness rather than the system.
//
// The prefix is therefore load-bearing rather than cosmetic: Table refuses any
// artefact carrying it. A smoke run that quietly rendered as a finding would be
// the most damaging possible failure of this project, because the numbers would
// look entirely reasonable.
const BrokerFake = "fake:"

// ErrNotAMeasurement is returned when artefacts came from an in-process broker.
type ErrNotAMeasurement struct {
	RunID  string
	Broker string
}

func (e *ErrNotAMeasurement) Error() string {
	return fmt.Sprintf(
		"ablation: run %s used broker %q, which is in-process; it verifies the "+
			"harness but cannot measure delivery semantics under broker loss. "+
			"Run against a real cluster (make up-chaos && make ablate).",
		e.RunID, e.Broker)
}

// IsMeasurement reports whether an artefact came from a real broker.
func (a Artefact) IsMeasurement() bool {
	return !strings.HasPrefix(a.BrokerVersion, BrokerFake)
}

// Row is one line of the Finding 2 table: the median of >= 3 runs, with the
// min-max range, per the findings-report skill.
type Row struct {
	Config        string `json:"config"`
	Runs          int    `json:"runs"`
	Sent          string `json:"sent"`
	Applied       string `json:"applied"`
	Lost          string `json:"lost"`
	Duplicated    string `json:"duplicated"`
	P50           string `json:"p50"`
	P95           string `json:"p95"`
	P99           string `json:"p99"`
	LagPeak       string `json:"lag_peak"`
	DrainSeconds  string `json:"drain_seconds"`
	InvariantHeld bool   `json:"invariant_held"`
}

// MinRuns is the minimum number of runs per configuration a table may be built
// from. A single run of a chaotic system is an anecdote.
const MinRuns = 3

// Table folds artefacts into one row per configuration.
func Table(artefacts []Artefact, minRuns int) ([]Row, error) {
	if len(artefacts) == 0 {
		return nil, &ErrMismatchedParameters{Detail: "no artefacts found"}
	}
	// Checked before the fixed-parameter guard: a set of smoke runs is
	// perfectly self-consistent, so the parameter check would pass it happily
	// and the table would render numbers that mean nothing.
	for _, a := range artefacts {
		if !a.IsMeasurement() {
			return nil, &ErrNotAMeasurement{RunID: a.RunID, Broker: a.BrokerVersion}
		}
	}
	key := artefacts[0].fixedKey()
	for _, a := range artefacts[1:] {
		if a.fixedKey() != key {
			return nil, &ErrMismatchedParameters{
				Detail: fmt.Sprintf("run %s (config %s) does not match run %s (config %s)",
					a.RunID, a.Config, artefacts[0].RunID, artefacts[0].Config),
			}
		}
	}

	byConfig := map[consumer.Mode][]Artefact{}
	for _, a := range artefacts {
		byConfig[a.Config] = append(byConfig[a.Config], a)
	}

	configs := make([]consumer.Mode, 0, len(byConfig))
	for c := range byConfig {
		configs = append(configs, c)
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i] < configs[j] })

	rows := make([]Row, 0, len(configs))
	for _, c := range configs {
		runs := byConfig[c]
		if len(runs) < minRuns {
			return nil, &ErrMismatchedParameters{
				Detail: fmt.Sprintf("configuration %s has %d runs, need at least %d",
					c, len(runs), minRuns),
			}
		}
		held := true
		for _, r := range runs {
			if !r.InvariantHeld {
				held = false
			}
		}
		rows = append(rows, Row{
			Config:        string(c),
			Runs:          len(runs),
			Sent:          stat(runs, func(a Artefact) int64 { return a.Sent }),
			Applied:       stat(runs, func(a Artefact) int64 { return a.Applied }),
			Lost:          stat(runs, func(a Artefact) int64 { return a.Lost }),
			Duplicated:    stat(runs, func(a Artefact) int64 { return a.Duplicated }),
			P50:           statMicros(runs, func(a Artefact) int64 { return a.P50 }),
			P95:           statMicros(runs, func(a Artefact) int64 { return a.P95 }),
			P99:           statMicros(runs, func(a Artefact) int64 { return a.P99 }),
			LagPeak:       stat(runs, func(a Artefact) int64 { return a.LagPeak }),
			DrainSeconds:  statFloat(runs, func(a Artefact) float64 { return a.DrainSeconds }),
			InvariantHeld: held,
		})
	}
	return rows, nil
}

func medianMinMax(values []int64) (med, lo, hi int64) {
	s := append([]int64(nil), values...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2], s[0], s[len(s)-1]
}

func stat(runs []Artefact, get func(Artefact) int64) string {
	vals := make([]int64, 0, len(runs))
	for _, r := range runs {
		vals = append(vals, get(r))
	}
	med, lo, hi := medianMinMax(vals)
	if lo == hi {
		return fmt.Sprintf("%d", med)
	}
	return fmt.Sprintf("%d [%d–%d]", med, lo, hi)
}

func statMicros(runs []Artefact, get func(Artefact) int64) string {
	vals := make([]int64, 0, len(runs))
	for _, r := range runs {
		vals = append(vals, get(r))
	}
	med, lo, hi := medianMinMax(vals)
	f := func(micros int64) string {
		return (time.Duration(micros) * time.Microsecond).String()
	}
	if lo == hi {
		return f(med)
	}
	return fmt.Sprintf("%s [%s–%s]", f(med), f(lo), f(hi))
}

func statFloat(runs []Artefact, get func(Artefact) float64) string {
	vals := make([]int64, 0, len(runs))
	for _, r := range runs {
		vals = append(vals, int64(get(r)*1000))
	}
	med, lo, hi := medianMinMax(vals)
	f := func(milli int64) string { return fmt.Sprintf("%.1f", float64(milli)/1000) }
	if lo == hi {
		return f(med)
	}
	return fmt.Sprintf("%s [%s–%s]", f(med), f(lo), f(hi))
}
