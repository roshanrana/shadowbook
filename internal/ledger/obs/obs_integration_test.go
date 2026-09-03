//go:build integration

package obs_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

func setup(t *testing.T) (*store.Store, []uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("SHADOWBOOK_LEDGER_DSN")
	if dsn == "" {
		t.Skip("SHADOWBOOK_LEDGER_DSN unset")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	acc := []uuid.UUID{
		uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}
	for _, id := range acc {
		if err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: id, ProductCode: "CHK-01", Currency: "USD",
			OpenedOn: bizdate.Date(2018, time.June, 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st, acc
}

// The invariant is exported as a function precisely so tests assert it directly
// rather than scraping it -- a test that scrapes can pass while the ledger is
// wrong.
func TestCheckInvariantOnAHealthyLedger(t *testing.T) {
	st, acc := setup(t)
	ctx := context.Background()

	res, err := obs.CheckInvariant(ctx, st.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Err() != nil {
		t.Fatalf("empty ledger reported broken: %v", res.Drift)
	}

	svc := posting.New(st)
	if _, err := svc.Post(ctx, posting.Request{
		Principal: "sim", IdempotencyKey: "k1", Kind: "transfer", Currency: "USD",
		BusinessDate: bizdate.Date(2028, time.February, 29),
		ValueDate:    bizdate.Date(2028, time.February, 29),
		PostedAt:     time.Date(2028, time.February, 29, 10, 0, 0, 0, time.UTC),
		Entries: []posting.EntryRequest{
			{AccountID: acc[0], AmountMinor: -125000},
			{AccountID: acc[1], AmountMinor: 125000},
		},
	}); err != nil {
		t.Fatal(err)
	}

	res, err = obs.CheckInvariant(ctx, st.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("balanced posting broke the invariant: %v", res.Drift)
	}
	if res.Drift["USD"] != 0 {
		t.Fatalf("USD drift = %d", res.Drift["USD"])
	}
}

// The invariant must be able to FAIL, or asserting it proves nothing. The DDL
// makes an unbalanced posting impossible through the posting path, so this
// checks the detector against a hand-built drift.
func TestInvariantResultReportsDrift(t *testing.T) {
	res := obs.InvariantResult{OK: false, Drift: map[string]int64{"USD": 17}}
	err := res.Err()
	if err == nil {
		t.Fatal("a broken invariant reported no error")
	}
	if !strings.Contains(err.Error(), "17") {
		t.Fatalf("error = %q; it must name the drift", err)
	}
	if (obs.InvariantResult{OK: true}).Err() != nil {
		t.Fatal("a healthy invariant reported an error")
	}
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}

func TestCheckerPublishesTheGaugesImmediately(t *testing.T) {
	st, _ := setup(t)
	reg := prometheus.NewRegistry()
	m := obs.NewMetrics(reg)
	checker := obs.NewChecker(st, m, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- checker.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for {
		if checker.Last().At != (time.Time{}) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the checker never published a result")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if got := gaugeValue(t, reg, "shadowbook_ledger_invariant_ok"); got != 1 {
		t.Fatalf("invariant gauge = %v, want 1", got)
	}
	// NFR-3: the check itself must be fast enough to stay within a second.
	if lag := checker.Last().Elapsed; lag > time.Second {
		t.Fatalf("invariant check took %v; NFR-3 budgets 1s", lag)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the checker did not stop on cancellation")
	}
}

func TestAllMetricsAreRegisteredAndNamed(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs.NewMetrics(reg)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range families {
		got[f.GetName()] = true
	}
	// Counters with no observations are absent from Gather, so check the ones
	// that always report.
	for _, name := range []string{
		"shadowbook_posting_duration_seconds",
		"shadowbook_ledger_invariant_ok",
		"shadowbook_ledger_invariant_last_check_seconds",
		"shadowbook_outbox_depth",
	} {
		if !got[name] {
			t.Fatalf("metric %s is not registered", name)
		}
	}
	var _ dto.MetricFamily
}
