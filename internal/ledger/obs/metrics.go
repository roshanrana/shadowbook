// Package obs is the ledger's observability surface: Prometheus metrics and the
// global-invariant checker.
//
// The invariant check is deliberately both a metric AND an exported function.
// FR-L9 wants the gauge; integration tests want to assert the invariant
// directly rather than scrape it, and a test that scrapes is a test that can
// pass while the ledger is wrong.
package obs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

// Metrics holds every collector the ledger exports.
type Metrics struct {
	PostingsTotal   *prometheus.CounterVec   // by kind and result
	PostingDuration prometheus.Histogram     // NFR-2: p99 <= 50ms
	InvariantOK     prometheus.Gauge         // FR-L9: 1 when every currency sums to zero
	InvariantAge    prometheus.Gauge         // NFR-3: seconds since the last check
	OutboxDepth     prometheus.Gauge         // unsent rows
	ConsumerLag     *prometheus.GaugeVec     // by delivery mode; Finding 2
	MovementsTotal  *prometheus.CounterVec   // by mode and result
	EODDuration     *prometheus.HistogramVec // by phase
}

// NewMetrics registers on reg. Passing a fresh registry per test keeps
// registrations from colliding.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		PostingsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shadowbook_postings_total",
			Help: "Postings written, by kind and result.",
		}, []string{"kind", "result"}),
		PostingDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "shadowbook_posting_duration_seconds",
			Help: "End-to-end posting latency.",
			// Buckets straddle NFR-2's 50ms p99 target so the SLO is readable
			// off the histogram rather than interpolated from far away.
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1},
		}),
		InvariantOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "shadowbook_ledger_invariant_ok",
			Help: "1 when SUM(amount_minor) is zero for every currency, else 0.",
		}),
		InvariantAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "shadowbook_ledger_invariant_last_check_seconds",
			Help: "Seconds since the invariant was last evaluated (NFR-3 targets <= 1).",
		}),
		OutboxDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "shadowbook_outbox_depth",
			Help: "Outbox rows not yet produced.",
		}),
		ConsumerLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "shadowbook_consumer_lag",
			Help: "Consumer lag in records, by delivery mode.",
		}, []string{"mode"}),
		MovementsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shadowbook_movements_total",
			Help: "Movements consumed, by mode and result.",
		}, []string{"mode", "result"}),
		EODDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "shadowbook_eod_duration_seconds",
			Help:    "End-of-day phase duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"phase"}),
	}
	reg.MustRegister(
		m.PostingsTotal, m.PostingDuration, m.InvariantOK, m.InvariantAge,
		m.OutboxDepth, m.ConsumerLag, m.MovementsTotal, m.EODDuration,
	)
	return m
}

// InvariantResult is one evaluation of the global zero-sum rule.
type InvariantResult struct {
	OK      bool
	Drift   map[string]int64 // per-currency sum; every entry must be zero
	At      time.Time
	Elapsed time.Duration
}

// Err returns a descriptive error when the invariant does not hold.
func (r InvariantResult) Err() error {
	if r.OK {
		return nil
	}
	return fmt.Errorf("ledger invariant broken: %v", r.Drift)
}

// CheckInvariant evaluates SUM(amount_minor) per currency. Exported so tests
// assert it directly after every scenario (CLAUDE.md).
func CheckInvariant(ctx context.Context, q store.Queryer) (InvariantResult, error) {
	start := time.Now()
	drift, err := store.GlobalInvariant(ctx, q)
	if err != nil {
		return InvariantResult{}, fmt.Errorf("obs: invariant query: %w", err)
	}
	ok := true
	for _, d := range drift {
		if d != 0 {
			ok = false
			break
		}
	}
	return InvariantResult{OK: ok, Drift: drift, At: start, Elapsed: time.Since(start)}, nil
}

// Checker runs the invariant on a ticker and publishes the gauges.
type Checker struct {
	st       *store.Store
	m        *Metrics
	interval time.Duration

	mu   sync.RWMutex
	last InvariantResult
}

// NewChecker builds the ticker. An interval at or below one second keeps
// NFR-3's "invariant lag <= 1s" achievable.
func NewChecker(st *store.Store, m *Metrics, interval time.Duration) *Checker {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &Checker{st: st, m: m, interval: interval}
}

// Last returns the most recent result.
func (c *Checker) Last() InvariantResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last
}

// Run evaluates until ctx is cancelled. Every blocking select has ctx.Done()
// (CLAUDE.md); Run returns nil on cancellation, which is a normal shutdown.
func (c *Checker) Run(ctx context.Context) error {
	t := time.NewTicker(c.interval)
	defer t.Stop()

	c.once(ctx) // publish immediately so the gauge is never stale at start-up
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			c.once(ctx)
		}
	}
}

func (c *Checker) once(ctx context.Context) {
	res, err := CheckInvariant(ctx, c.st.Pool)
	if err != nil {
		// A failed check is not a satisfied invariant. Report 0, never 1.
		c.m.InvariantOK.Set(0)
		return
	}
	c.mu.Lock()
	c.last = res
	c.mu.Unlock()

	if res.OK {
		c.m.InvariantOK.Set(1)
	} else {
		c.m.InvariantOK.Set(0)
	}
	c.m.InvariantAge.Set(res.Elapsed.Seconds())

	if depth, err := store.OutboxDepth(ctx, c.st.Pool); err == nil {
		c.m.OutboxDepth.Set(float64(depth))
	}
}
