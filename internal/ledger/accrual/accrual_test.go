package accrual_test

import (
	"testing"
	"time"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/accrual"
)

// Interest is computed by summing daily balances first and rounding ONCE, so
// per-day rounding cannot accumulate. Q1, Q3 and Q6 all diverge from this
// single function, so its arithmetic is worth pinning exactly.
func TestInterestFor(t *testing.T) {
	for _, tc := range []struct {
		name              string
		dailySum          int64
		rateBP            int64
		den               int64
		want              int64
		wantDiffersAct360 bool
	}{
		{name: "no rate", dailySum: 1_000_000, rateBP: 0, den: 365, want: 0},
		{name: "no balance", dailySum: 0, rateBP: 325, den: 365, want: 0},
		{
			// $10,000 held for 31 days at 3.25%:
			// 1_000_000 * 31 * 325 / (10000 * 365) = 2760.27 -> 2760
			name:     "a month at 3.25% ACT/365",
			dailySum: 1_000_000 * 31, rateBP: 325, den: 365, want: 2760,
		},
		{
			// Same money, leap year: denominator 366 gives slightly less.
			name:     "same month ACT/ACT in a leap year",
			dailySum: 1_000_000 * 31, rateBP: 325, den: 366, want: 2753,
		},
		{
			// Q3's basis: 360 gives MORE than 365. This gap is the break.
			name:     "Q3's ACT/360 pays more",
			dailySum: 1_000_000 * 31, rateBP: 325, den: 360, want: 2799,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := accrual.InterestFor(tc.dailySum, tc.rateBP, tc.den); got != tc.want {
				t.Fatalf("InterestFor(%d, %d, %d) = %d, want %d",
					tc.dailySum, tc.rateBP, tc.den, got, tc.want)
			}
		})
	}
}

// The three bases must produce three different numbers, or Q3 and Q6 cannot be
// detected at the account-day grain.
func TestBasesDivergeOrQuirksAreUndetectable(t *testing.T) {
	const sum, rate = 1_000_000 * 31, int64(325)
	act365 := accrual.InterestFor(sum, rate, 365)
	actact := accrual.InterestFor(sum, rate, 366)
	act360 := accrual.InterestFor(sum, rate, 360)

	if act365 == act360 {
		t.Fatal("ACT/365 and ACT/360 agree: Q3 would be undetectable")
	}
	if act365 == actact {
		t.Fatal("ACT/365 and ACT/ACT agree in a leap year: Q6 would be undetectable")
	}
	if act360 <= act365 || act365 <= actact {
		t.Fatalf("expected ACT/360 > ACT/365 > ACT/ACT, got %d, %d, %d", act360, act365, actact)
	}
}

// The documented basis is chosen by one function, and it must pick ACT/ACT in
// 2028 -- the year both demo windows live in.
func TestDocumentedBasisInTheDemoYear(t *testing.T) {
	if got := bizdate.BasisFor(bizdate.Date(2028, time.February, 1)); got != bizdate.ACTACT {
		t.Fatalf("February 2028 basis = %v, want ACT/ACT", got)
	}
	_, den := bizdate.DayCountFraction(
		bizdate.Date(2028, time.February, 1), bizdate.Date(2028, time.February, 2), bizdate.ACTACT)
	if den != 366 {
		t.Fatalf("2028 ACT/ACT denominator = %d, want 366", den)
	}
}

func TestProductCatalogueIsCoherent(t *testing.T) {
	for code, p := range accrual.Products {
		if p.Code != code {
			t.Fatalf("product %q has Code %q", code, p.Code)
		}
		if p.MinBalanceFeeMinor > 0 && p.MinBalanceMinor <= 0 {
			t.Fatalf("%s charges a minimum-balance fee with no threshold", code)
		}
		if p.RateBP < 0 || p.MonthlyFeeMinor < 0 {
			t.Fatalf("%s has a negative rate or fee", code)
		}
	}
	// SAV-01 is the product Q3 targets, so it must actually pay interest.
	if accrual.Products["SAV-01"].RateBP == 0 {
		t.Fatal("SAV-01 pays no interest; Q3 would be undetectable")
	}
	// CHK-01 is the product Q4 and Q7 target, so it must charge both fees.
	chk := accrual.Products["CHK-01"]
	if chk.MonthlyFeeMinor == 0 {
		t.Fatal("CHK-01 has no monthly fee; Q4 would be undetectable")
	}
	if chk.MinBalanceFeeMinor == 0 {
		t.Fatal("CHK-01 has no minimum-balance fee; Q7 would be undetectable")
	}
}
