//go:build perf

// Package perf holds the performance smoke test for NFR-1 and NFR-2.
//
// It is behind its own build tag and is NOT part of `make check`: a latency
// assertion inside the correctness gate would make the gate flaky on a busy
// machine, and a flaky gate gets ignored. `make perf` runs it deliberately.
//
// It drives the real HTTP API through the real posting path against a real
// PostgreSQL, using the same vegeta pacer and targeter the harness uses -- so
// what it measures is the thing the ablation would measure, minus the broker.
package perf_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	vegeta "github.com/tsenart/vegeta/v12/lib"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/harness/load"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
	"github.com/roshanrana/shadowbook/internal/ledger/httpapi"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/testsupport"
)

// NFR-2: p99 posting latency at steady state.
const p99Budget = 50 * time.Millisecond

func TestSteadyStateLatencyAndThroughput(t *testing.T) {
	st := testsupport.FreshStore(t)
	ctx := context.Background()
	if err := consumer.EnsureSuspenseAccounts(ctx, st, bizdate.Date(2028, time.January, 1)); err != nil {
		t.Fatal(err)
	}

	accounts := testsupport.SeedAccounts(t, st, 20)
	accounts = append(accounts, consumer.SuspenseAccountFor("USD")) // targeter uses the last as contra

	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(httpapi.New(httpapi.Config{
		Store: st, Metrics: obs.NewMetrics(reg), Registry: reg,
	}).Handler())
	defer srv.Close()

	// The default is a SMOKE rate, not the NFR-1 target. NFR-1 asks for
	// >= 2,000 postings/s at p99 <= 50ms on the owner's machine; what this
	// environment sustains is a different number and pretending otherwise
	// would be a fabricated result. Raise SHADOWBOOK_PERF_RATE to measure the
	// real box -- docs/ship-report.md records the sweep taken here.
	rate := envInt("SHADOWBOOK_PERF_RATE", 200)
	duration := time.Duration(envInt("SHADOWBOOK_PERF_SECONDS", 10)) * time.Second

	opts := load.Options{
		Profile: load.Steady, Rate: rate, Duration: duration, Seed: 20260903,
		Accounts: accounts, BaseURL: srv.URL, Currency: "USD",
		Date: bizdate.Date(2028, time.February, 29),
	}
	pacer, err := load.PacerFor(opts)
	if err != nil {
		t.Fatal(err)
	}
	targeter, err := load.TargeterFor(opts)
	if err != nil {
		t.Fatal(err)
	}

	attacker := vegeta.NewAttacker(vegeta.Workers(32), vegeta.MaxWorkers(256))
	var (
		latencies []time.Duration
		ok, bad   int
	)
	for res := range attacker.Attack(targeter, pacer, duration, "steady") {
		latencies = append(latencies, res.Latency)
		if res.Code == 201 {
			ok++
		} else {
			bad++
			if bad <= 3 {
				t.Logf("non-201: code=%d error=%q body=%s", res.Code, res.Error, truncate(res.Body))
			}
		}
	}
	if len(latencies) == 0 {
		t.Fatal("no requests were made")
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p := func(q float64) time.Duration { return latencies[int(float64(len(latencies)-1)*q)] }

	achieved := float64(ok) / duration.Seconds()
	t.Logf("requests=%d ok=%d failed=%d", len(latencies), ok, bad)
	t.Logf("throughput=%.0f postings/s (offered %d/s)", achieved, rate)
	t.Logf("p50=%v p95=%v p99=%v max=%v", p(0.50), p(0.95), p(0.99), latencies[len(latencies)-1])

	if bad > 0 {
		t.Fatalf("%d of %d requests failed; a latency number from a failing run means nothing",
			bad, len(latencies))
	}

	// Every posting must have landed, and the ledger must still balance.
	var entries int64
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != int64(ok)*2 {
		t.Fatalf("entries = %d, want %d (two per posting)", entries, ok*2)
	}
	res, err := obs.CheckInvariant(ctx, st.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Err(); err != nil {
		t.Fatalf("invariant broken under load: %v", err)
	}

	if got := p(0.99); got > p99Budget {
		t.Errorf("p99 = %v at %d postings/s offered, NFR-2 budgets %v.\n"+
			"  Every request succeeded and the invariant held, so this is a capacity\n"+
			"  result, not a correctness one: the machine cannot serve this rate\n"+
			"  within the latency budget.", got, rate, p99Budget)
	}
}

func truncate(b []byte) string {
	if len(b) > 160 {
		return string(b[:160]) + "..."
	}
	return string(b)
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

var _ = uuid.Nil
var _ = store.ErrNotFound
