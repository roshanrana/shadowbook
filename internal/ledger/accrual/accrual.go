// Package accrual runs end of day: hold expiry, interest, fees and checkpoints.
//
// The phase order is fixed and tested: expire -> interest -> fees -> checkpoint.
// Fees must see post-expiry available balance, which is exactly what Q7
// diverges on, so reordering these phases would change what Finding 1 measures.
//
// EOD is triggered by the harness, never by wall-clock time (LLD §8.3), and is
// idempotent per business date: a replay raises 23505 on eod_runs_pkey.
package accrual

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/balance"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

// basisPointDenominator converts basis points to a fraction: 325bp = 325/10000.
const basisPointDenominator = 10000

// Engine runs EOD.
type Engine struct {
	st  *store.Store
	cal bizdate.Calendar
	bal *balance.Service
	pst *posting.Service
}

// New builds the engine.
func New(st *store.Store, cal bizdate.Calendar) *Engine {
	return &Engine{st: st, cal: cal, bal: balance.New(st), pst: posting.New(st)}
}

// Report is what one EOD did, and is stored on the eod_runs row.
type Report struct {
	BusinessDate    string `json:"business_date"`
	HoldsExpired    int64  `json:"holds_expired"`
	InterestPosted  int    `json:"interest_posted"`
	FeesPosted      int    `json:"fees_posted"`
	Checkpoints     int    `json:"checkpoints"`
	InterestMinor   int64  `json:"interest_minor_total"`
	FeeMinor        int64  `json:"fee_minor_total"`
	DurationSeconds string `json:"duration"`
}

// Run executes end of day for a business date.
//
// `now` is the instant hold expiry is judged against and is passed in, not
// read: EOD must be reproducible from a seed (NFR-5).
func (e *Engine) Run(ctx context.Context, d bizdate.BusinessDate, now time.Time) (Report, error) {
	start := time.Now()
	rep := Report{BusinessDate: d.String()}

	// Claim the run. A replay is a 23505 the caller maps to EODAlreadyRun.
	if err := store.ClaimEODRun(ctx, e.st.Pool, d); err != nil {
		return rep, fmt.Errorf("accrual: claim eod run: %w", err)
	}

	// Phase 1 -- hold expiry, BEFORE fees.
	expired, err := e.bal.ExpireDue(ctx, now)
	if err != nil {
		return rep, fmt.Errorf("accrual: expire holds: %w", err)
	}
	rep.HoldsExpired = expired

	accounts, err := store.AllAccounts(ctx, e.st.Pool)
	if err != nil {
		return rep, fmt.Errorf("accrual: list accounts: %w", err)
	}

	// Phase 2 -- interest for the month just ended.
	//
	// EOD only runs on business days, but the documented posting date is the
	// CALENDAR first (FR-L6), which may be a weekend -- 1 April 2028 is a
	// Saturday. So the trigger is the first business day on or after the
	// calendar first, while the posting is DATED the calendar first. Q12 is the
	// legacy core dating it on the trigger day instead; if this ran only when
	// the two coincided, Q12 could never produce a break.
	if d.Equal(e.cal.FirstBusinessDayOfMonth(d.Y, d.M)) {
		n, total, err := e.postInterest(ctx, accounts, d)
		if err != nil {
			return rep, err
		}
		rep.InterestPosted, rep.InterestMinor = n, total
	}

	// Phase 3 -- fees at month end.
	if e.isMonthEnd(d) {
		n, total, err := e.postFees(ctx, accounts, d, now)
		if err != nil {
			return rep, err
		}
		rep.FeesPosted, rep.FeeMinor = n, total
	}

	// Phase 4 -- checkpoints, so the derived balance read stays bounded.
	for _, a := range accounts {
		if err := e.bal.Checkpoint(ctx, a.ID, d); err != nil {
			if store.IsUniqueViolation(err, "") {
				continue // already checkpointed for this account-day
			}
			return rep, fmt.Errorf("accrual: checkpoint %s: %w", a.ID, err)
		}
		rep.Checkpoints++
	}

	rep.DurationSeconds = time.Since(start).String()
	blob, err := json.Marshal(rep)
	if err != nil {
		return rep, fmt.Errorf("accrual: marshal report: %w", err)
	}
	if err := store.FinishEODRun(ctx, e.st.Pool, d, blob); err != nil {
		return rep, fmt.Errorf("accrual: finish eod run: %w", err)
	}
	return rep, nil
}

// isMonthEnd reports whether the next calendar day falls in a different month.
//
// The obvious formulation -- comparing d.AddDays(1) with Date(d.Y, d.M, d.D+1)
// -- is always false, because Date normalises 32 March to 1 April and the two
// sides agree by construction. It silently meant no fee was ever assessed.
func (e *Engine) isMonthEnd(d bizdate.BusinessDate) bool {
	return d.AddDays(1).M != d.M
}

// InterestFor computes a month's interest exactly.
//
// The daily balances are summed FIRST and rounded ONCE, so no intermediate
// rounding accumulates:
//
//	interest = round_half_even( SUM_days(balance_d) * rate_bp , 10000 * den )
//
// den comes from the documented basis for the month: ACT/365, or ACT/ACT (366)
// in a leap year. Q3 uses ACT/360 on SAV-01; Q6 stays on 365 through a leap
// year. Both diverge from this one function.
func InterestFor(dailyBalanceSum int64, rateBP int64, den int64) int64 {
	if rateBP == 0 || dailyBalanceSum == 0 {
		return 0
	}
	return bizdate.RoundHalfEven(dailyBalanceSum*rateBP, basisPointDenominator*den)
}

func (e *Engine) postInterest(ctx context.Context, accounts []store.Account, postOn bizdate.BusinessDate) (int, int64, error) {
	// Interest is for the month that just ended.
	// postOn is the trigger day; the posting is dated the calendar first.
	calendarFirst := e.cal.FirstOfMonth(postOn.Y, postOn.M)
	prevMonthEnd := calendarFirst.AddDays(-1)
	first := bizdate.Date(prevMonthEnd.Y, prevMonthEnd.M, 1)
	basis := bizdate.BasisFor(first)
	_, den := bizdate.DayCountFraction(first, first.AddDays(1), basis)

	var (
		count int
		total int64
	)
	for _, a := range accounts {
		p, ok := Products[a.ProductCode]
		if !ok || p.RateBP == 0 {
			continue
		}
		var sum int64
		for d := first; !d.After(prevMonthEnd); d = d.AddDays(1) {
			bal, err := store.LedgerBalance(ctx, e.st.Pool, a.ID, d)
			if err != nil {
				return count, total, fmt.Errorf("accrual: balance %s on %s: %w", a.ID, d, err)
			}
			if bal > 0 { // no interest on an overdrawn balance
				sum += bal
			}
		}
		amount := InterestFor(sum, p.RateBP, den)
		if amount == 0 {
			continue
		}
		if err := e.postAgainstSuspense(ctx, a, amount, "interest", calendarFirst,
			fmt.Sprintf("interest/%s/%04d-%02d", a.ID, first.Y, int(first.M))); err != nil {
			return count, total, err
		}
		count++
		total += amount
	}
	return count, total, nil
}

func (e *Engine) postFees(ctx context.Context, accounts []store.Account, d bizdate.BusinessDate, now time.Time) (int, int64, error) {
	var (
		count int
		total int64
	)
	for _, a := range accounts {
		p, ok := Products[a.ProductCode]
		if !ok {
			continue
		}
		fee := p.MonthlyFeeMinor // documented: no waiver by open date (Q4 waives)

		if p.MinBalanceFeeMinor > 0 {
			// Documented: the threshold is tested against AVAILABLE balance.
			// Q7 tests the ledger balance instead.
			b, err := e.bal.At(ctx, a.ID, d, now)
			if err != nil {
				return count, total, fmt.Errorf("accrual: balance %s: %w", a.ID, err)
			}
			if b.Available < p.MinBalanceMinor {
				fee += p.MinBalanceFeeMinor
			}
		}
		if fee == 0 {
			continue
		}
		if err := e.postAgainstSuspense(ctx, a, -fee, "fee", d,
			fmt.Sprintf("fee/%s/%s", a.ID, d)); err != nil {
			return count, total, err
		}
		count++
		total += fee
	}
	return count, total, nil
}

// postAgainstSuspense books a one-sided amount as a balanced posting.
func (e *Engine) postAgainstSuspense(ctx context.Context, a store.Account, amountMinor int64,
	kind string, d bizdate.BusinessDate, idemKey string) error {
	suspense := suspenseFor(a.Currency)
	_, err := e.pst.Post(ctx, posting.Request{
		Principal: "eod", IdempotencyKey: idemKey, Kind: kind, Currency: a.Currency,
		BusinessDate: d, ValueDate: d,
		PostedAt: time.Date(d.Y, d.M, d.D, 16, 0, 0, 0, time.UTC),
		Entries: []posting.EntryRequest{
			{AccountID: a.ID, AmountMinor: amountMinor},
			{AccountID: suspense, AmountMinor: -amountMinor},
		},
	})
	if err != nil {
		return fmt.Errorf("accrual: post %s for %s: %w", kind, a.ID, err)
	}
	return nil
}

var suspenseNamespace = uuid.MustParse("9c4e5b18-27f3-5a6d-b0e4-1d7f8c3a5b92")

func suspenseFor(cur money.Currency) uuid.UUID {
	return uuid.NewSHA1(suspenseNamespace, []byte("suspense/"+string(cur)))
}
