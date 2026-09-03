// Package load builds the open-model load profiles of FR-H1.
//
// vegeta drives HTTP and exposes exactly two extension points -- a Pacer, which
// decides WHEN the next request goes, and a Targeter, which decides WHAT it is.
// Both are small interfaces, so the four named profiles are ordinary Go we can
// unit-test with a fixed seed rather than a load script we can only observe.
// That was the argument for choosing vegeta over k6 (D-005); this file is
// where it is paid off.
package load

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	vegeta "github.com/tsenart/vegeta/v12/lib"

	"github.com/roshanrana/shadowbook/internal/bizdate"
)

// Profile is a named load shape.
type Profile string

const (
	// Steady is a flat arrival rate: the NFR-1 measurement.
	Steady Profile = "steady"
	// Payday is a spike: a sine around a mean, so the ledger meets a burst it
	// did not choose the timing of.
	Payday Profile = "payday"
	// MonthEnd ramps into the close and biases toward fee-eligible accounts.
	MonthEnd Profile = "month-end"
	// HotKey sends at a flat rate but concentrates >= 20% of traffic on two
	// accounts, which stresses per-account ordering rather than throughput.
	HotKey Profile = "hot-key"
)

// Valid reports whether p is one of the four.
func (p Profile) Valid() bool {
	switch p {
	case Steady, Payday, MonthEnd, HotKey:
		return true
	}
	return false
}

// Options configure a profile.
type Options struct {
	Profile  Profile
	Rate     int           // requests per second (the mean, for Payday)
	Duration time.Duration // used by ramping profiles to place themselves
	Seed     int64
	Accounts []uuid.UUID
	BaseURL  string
	Currency string
	Date     bizdate.BusinessDate
}

// PacerFor returns the arrival-rate shape.
//
// Every pacer here is OPEN-MODEL: arrivals are decided by the clock, not by
// when the previous response came back. A closed-model generator would throttle
// itself the moment the ledger slowed down, which is precisely the behaviour
// under load we are trying to measure.
func PacerFor(o Options) (vegeta.Pacer, error) {
	if !o.Profile.Valid() {
		return nil, fmt.Errorf("load: unknown profile %q", o.Profile)
	}
	if o.Rate <= 0 {
		return nil, fmt.Errorf("load: rate must be positive, got %d", o.Rate)
	}
	rate := vegeta.Rate{Freq: o.Rate, Per: time.Second}

	switch o.Profile {
	case Steady, HotKey:
		return rate, nil
	case Payday:
		// Amplitude below the mean so the trough never reaches zero: a payday
		// spike is a busy day, not an outage followed by a flood.
		return vegeta.SinePacer{
			Period: max(o.Duration, time.Minute),
			Mean:   rate,
			Amp:    vegeta.Rate{Freq: o.Rate / 2, Per: time.Second},
		}, nil
	case MonthEnd:
		return &RampPacer{Start: rate, End: vegeta.Rate{Freq: o.Rate * 2, Per: time.Second}, Over: o.Duration}, nil
	default:
		return nil, fmt.Errorf("load: unknown profile %q", o.Profile)
	}
}

// RampPacer accelerates linearly from Start to End over Over.
//
// vegeta ships constant and sine pacers; a month-end close is neither. It is a
// ramp into a deadline, so this is the shape.
type RampPacer struct {
	Start vegeta.Rate
	End   vegeta.Rate
	Over  time.Duration
}

// Rate implements vegeta.Pacer: the instantaneous rate at `elapsed`, in hits
// per second. vegeta uses it for reporting, not for scheduling.
func (r *RampPacer) Rate(elapsed time.Duration) float64 {
	if r.Over <= 0 {
		return 0
	}
	if elapsed >= r.Over {
		return float64(r.End.Freq)
	}
	frac := elapsed.Seconds() / r.Over.Seconds()
	return float64(r.Start.Freq) + float64(r.End.Freq-r.Start.Freq)*frac
}

// Pace implements vegeta.Pacer.
func (r *RampPacer) Pace(elapsed time.Duration, hits uint64) (time.Duration, bool) {
	if r.Over <= 0 || r.Start.Freq <= 0 {
		return 0, true
	}
	if elapsed >= r.Over {
		return 0, true
	}
	// Hits expected by `elapsed` under a linear ramp is the area under it:
	//   f(t) = start + (end-start) * t/Over
	//   area = start*t + (end-start)*t^2 / (2*Over)
	seconds := elapsed.Seconds()
	over := r.Over.Seconds()
	expected := float64(r.Start.Freq)*seconds +
		float64(r.End.Freq-r.Start.Freq)*seconds*seconds/(2*over)
	if float64(hits) < expected {
		return 0, false
	}
	// Wait one interval at the CURRENT instantaneous rate.
	current := float64(r.Start.Freq) + float64(r.End.Freq-r.Start.Freq)*seconds/over
	if current <= 0 {
		return 0, true
	}
	return time.Duration(float64(time.Second) / current), false
}

// HotKeyShare is the fraction of traffic the hot-key profile concentrates on
// its two accounts. The README promises >= 20%.
const HotKeyShare = 0.30

// TargeterFor returns the request builder for a profile.
//
// Deterministic: every choice is a hash of (seed, request number), so a run is
// reproducible and a test can assert the exact distribution rather than
// sampling it.
func TargeterFor(o Options) (vegeta.Targeter, error) {
	if !o.Profile.Valid() {
		return nil, fmt.Errorf("load: unknown profile %q", o.Profile)
	}
	if len(o.Accounts) < 3 {
		return nil, fmt.Errorf("load: need at least 3 accounts, got %d", len(o.Accounts))
	}
	currency := o.Currency
	if currency == "" {
		currency = "USD"
	}
	suspense := o.Accounts[len(o.Accounts)-1]
	hot := o.Accounts[:2]

	// vegeta calls a Targeter from EVERY worker goroutine concurrently, so the
	// sequence number must be taken atomically and then used as a local value.
	//
	// A plain n++ followed by reads of n is not merely racy in the abstract: a
	// request could take its idempotency key from one sequence number and its
	// amount from another, producing the same key with a DIFFERENT body. The
	// ledger correctly answered 409 IdempotencyBodyMismatch, and the load test
	// looked like a ledger fault when it was a generator fault.
	var counter atomic.Uint64
	return func(t *vegeta.Target) error {
		if t == nil {
			return vegeta.ErrNilTarget
		}
		n := counter.Add(1)
		account := selectAccount(o, hot, n)
		// The modulus bounds this well inside int64, but make the narrowing
		// explicit rather than relying on the reader to check.
		amount := 1_000 + int64(det(o.Seed, "amt", n)%250_000) //nolint:gosec // bounded by the modulus

		body, err := json.Marshal(map[string]any{
			"kind":                "transfer",
			"currency":            currency,
			"business_date":       o.Date.String(),
			"value_date":          o.Date.String(),
			"posted_at":           time.Date(o.Date.Y, o.Date.M, o.Date.D, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
			"reverses_posting_id": nil,
			"entries": []map[string]any{
				{"account_id": account.String(), "amount_minor": amount},
				{"account_id": suspense.String(), "amount_minor": -amount},
			},
		})
		if err != nil {
			return err
		}

		t.Method = http.MethodPost
		t.URL = o.BaseURL + "/v1/postings"
		t.Body = body
		t.Header = http.Header{
			"Content-Type": []string{"application/json"},
			"X-Principal":  []string{"sim"},
			// A distinct key per request: the load test measures the posting
			// path, not the replay path. Reusing keys would silently turn a
			// throughput test into an idempotency-lookup test.
			"Idempotency-Key": []string{fmt.Sprintf("load-%s-%d-%d", o.Profile, o.Seed, n)},
		}
		return nil
	}, nil
}

func selectAccount(o Options, hot []uuid.UUID, n uint64) uuid.UUID {
	pool := o.Accounts[:len(o.Accounts)-1] // last one is the suspense account

	switch o.Profile {
	case HotKey:
		if float64(det(o.Seed, "hot", n)%1000)/1000.0 < HotKeyShare {
			return hot[det(o.Seed, "which", n)%2]
		}
	case MonthEnd:
		// Bias to the first half of the book, which is where the
		// fee-eligible products live in the generated account order.
		half := len(pool) / 2
		if half > 0 && det(o.Seed, "bias", n)%100 < 70 {
			return pool[int(det(o.Seed, "acct", n)%uint64(half))] //nolint:gosec // half > 0
		}
	case Steady, Payday:
	}
	return pool[int(det(o.Seed, "acct", n)%uint64(len(pool)))] //nolint:gosec // len(pool) > 0
}

func det(seed int64, label string, n uint64) uint64 {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d/%s/%d", seed, label, n)
	return h.Sum64()
}
