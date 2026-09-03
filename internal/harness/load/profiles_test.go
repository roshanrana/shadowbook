package load

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	vegeta "github.com/tsenart/vegeta/v12/lib"

	"github.com/roshanrana/shadowbook/internal/bizdate"
)

func accounts(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.NewSHA1(uuid.Nil, []byte{byte(i)})
	}
	return out
}

func opts(p Profile) Options {
	return Options{
		Profile: p, Rate: 100, Duration: 60 * time.Second, Seed: 7,
		Accounts: accounts(10), BaseURL: "http://localhost:8080",
		Currency: "USD", Date: bizdate.Date(2028, time.February, 29),
	}
}

func TestEveryProfileHasAPacerAndATargeter(t *testing.T) {
	for _, p := range []Profile{Steady, Payday, MonthEnd, HotKey} {
		t.Run(string(p), func(t *testing.T) {
			if _, err := PacerFor(opts(p)); err != nil {
				t.Fatalf("pacer: %v", err)
			}
			if _, err := TargeterFor(opts(p)); err != nil {
				t.Fatalf("targeter: %v", err)
			}
		})
	}
	if _, err := PacerFor(opts("nonsense")); err == nil {
		t.Fatal("an unknown profile was accepted")
	}
	if _, err := PacerFor(Options{Profile: Steady, Rate: 0}); err == nil {
		t.Fatal("a zero rate was accepted")
	}
}

// Open model: a pacer decides the next arrival from elapsed time and hits so
// far, and from nothing else -- the vegeta.Pacer signature guarantees it cannot
// see responses at all. A closed-model generator would throttle itself exactly
// when the ledger slowed down, which is the behaviour under load we are trying
// to measure.
//
// What is worth asserting is the consequence: falling behind never slows the
// generator down, and being ahead never ends the run early.
func TestPacersAreOpenModel(t *testing.T) {
	for _, p := range []Profile{Steady, Payday, MonthEnd} {
		pacer, err := PacerFor(opts(p))
		if err != nil {
			t.Fatal(err)
		}
		// Far behind schedule -> fire immediately, however far behind.
		for _, hits := range []uint64{0, 1, 10} {
			wait, stop := pacer.Pace(10*time.Second, hits)
			if stop || wait != 0 {
				t.Fatalf("%s: %d hits at 10s gave wait=%v stop=%v; want immediate",
					p, hits, wait, stop)
			}
		}
		// Ahead of schedule mid-run -> never ends the run.
		if _, stop := pacer.Pace(1*time.Second, 100_000); stop {
			t.Fatalf("%s: ended the run early because it was ahead of schedule", p)
		}
	}

	// The constant pacer is the one whose back-pressure shape we can pin
	// exactly: ahead of schedule, it waits.
	steady, _ := PacerFor(opts(Steady))
	if wait, stop := steady.Pace(time.Second, 100_000); stop || wait <= 0 {
		t.Fatalf("steady ahead of schedule: wait=%v stop=%v; want a positive wait", wait, stop)
	}
}

func TestRampPacerAccelerates(t *testing.T) {
	r := &RampPacer{
		Start: vegeta.Rate{Freq: 100, Per: time.Second},
		End:   vegeta.Rate{Freq: 200, Per: time.Second},
		Over:  100 * time.Second,
	}
	early, mid, late := r.Rate(0), r.Rate(50*time.Second), r.Rate(99*time.Second)
	if !(early < mid && mid < late) {
		t.Fatalf("ramp is not monotonic: %v %v %v", early, mid, late)
	}
	if math.Abs(early-100) > 0.001 || math.Abs(mid-150) > 0.001 {
		t.Fatalf("ramp values wrong: start=%v mid=%v", early, mid)
	}
	if _, stop := r.Pace(101*time.Second, 0); !stop {
		t.Fatal("ramp did not stop after its duration")
	}
}

// The README promises the hot-key profile puts >= 20% of traffic on two
// accounts. Asserted by counting, over a deterministic run.
func TestHotKeyConcentratesTraffic(t *testing.T) {
	o := opts(HotKey)
	tr, err := TargeterFor(o)
	if err != nil {
		t.Fatal(err)
	}
	hot := map[string]bool{o.Accounts[0].String(): true, o.Accounts[1].String(): true}

	const n = 5000
	hits := 0
	for i := 0; i < n; i++ {
		var target vegeta.Target
		if err := tr(&target); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Entries []struct {
				AccountID string `json:"account_id"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(target.Body, &body); err != nil {
			t.Fatal(err)
		}
		if hot[body.Entries[0].AccountID] {
			hits++
		}
	}
	share := float64(hits) / float64(n)
	if share < 0.20 {
		t.Fatalf("hot-key share = %.3f, README promises >= 0.20", share)
	}
	if share > 0.60 {
		t.Fatalf("hot-key share = %.3f; that is not a hot key, that is the whole book", share)
	}
}

func TestSteadyProfileSpreadsAcrossTheBook(t *testing.T) {
	o := opts(Steady)
	tr, _ := TargeterFor(o)
	seen := map[string]int{}
	for i := 0; i < 2000; i++ {
		var target vegeta.Target
		if err := tr(&target); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Entries []struct {
				AccountID string `json:"account_id"`
			} `json:"entries"`
		}
		_ = json.Unmarshal(target.Body, &body)
		seen[body.Entries[0].AccountID]++
	}
	// 9 selectable accounts (the last is the suspense account).
	if len(seen) < 9 {
		t.Fatalf("steady profile touched only %d accounts", len(seen))
	}
}

func TestTargeterIsDeterministic(t *testing.T) {
	a, _ := TargeterFor(opts(Steady))
	b, _ := TargeterFor(opts(Steady))
	for i := 0; i < 50; i++ {
		var ta, tb vegeta.Target
		if err := a(&ta); err != nil {
			t.Fatal(err)
		}
		if err := b(&tb); err != nil {
			t.Fatal(err)
		}
		if string(ta.Body) != string(tb.Body) {
			t.Fatalf("request %d differs between two identically seeded targeters", i)
		}
	}
}

// Every request carries a DISTINCT idempotency key. Reusing keys would turn a
// throughput measurement into an idempotency-lookup measurement.
func TestEachRequestHasADistinctIdempotencyKey(t *testing.T) {
	tr, _ := TargeterFor(opts(Steady))
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		var target vegeta.Target
		if err := tr(&target); err != nil {
			t.Fatal(err)
		}
		k := target.Header.Get("Idempotency-Key")
		if k == "" {
			t.Fatal("no idempotency key on a mutating request")
		}
		if seen[k] {
			t.Fatalf("idempotency key %q reused", k)
		}
		seen[k] = true
	}
}

func TestTargetsAreBalancedPostings(t *testing.T) {
	tr, _ := TargeterFor(opts(Steady))
	for i := 0; i < 100; i++ {
		var target vegeta.Target
		if err := tr(&target); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Entries []struct {
				AmountMinor int64 `json:"amount_minor"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(target.Body, &body); err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, e := range body.Entries {
			sum += e.AmountMinor
		}
		if sum != 0 {
			t.Fatalf("generated an unbalanced posting summing to %d", sum)
		}
	}
}

func TestTargeterNeedsEnoughAccounts(t *testing.T) {
	o := opts(Steady)
	o.Accounts = accounts(2)
	if _, err := TargeterFor(o); err == nil {
		t.Fatal("accepted a book too small to have a suspense account and a counterparty")
	}
}
