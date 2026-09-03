package accrual

import "github.com/roshanrana/shadowbook/internal/money"

// Product is the shadow's documented product configuration.
//
// Four of the twelve quirks are about these numbers being applied differently
// by the legacy core: Q3 (basis on SAV-01), Q4 (fee waiver by open date),
// Q7 (fee basis), Q12 (posting date). The values here are the DOCUMENTED ones.
type Product struct {
	Code string
	// RateBP is the annual interest rate in basis points. Zero means no interest.
	RateBP int64
	// MonthlyFeeMinor is charged at month end to every account.
	// Q4 is the legacy core waiving it for accounts opened before 2019-01-01;
	// the documented rule has no waiver.
	MonthlyFeeMinor int64
	// MinBalanceMinor is the threshold below which MinBalanceFeeMinor applies.
	// The threshold is tested against AVAILABLE balance (FR-L4); Q7 is the
	// legacy core testing it against the ledger balance.
	MinBalanceMinor    int64
	MinBalanceFeeMinor int64
	Currency           money.Currency
}

// Products is the documented catalogue. legacy-sim generates accounts against
// these codes.
var Products = map[string]Product{
	"CHK-01": {
		Code: "CHK-01", RateBP: 0,
		MonthlyFeeMinor: 500,                              // $5.00
		MinBalanceMinor: 100000, MinBalanceFeeMinor: 1200, // $1,000 threshold, $12.00 fee
		Currency: "USD",
	},
	"SAV-01": {
		Code: "SAV-01", RateBP: 325, // 3.25% annual
		MonthlyFeeMinor: 0,
		MinBalanceMinor: 0, MinBalanceFeeMinor: 0,
		Currency: "USD",
	},
	"CHK-JPY": {
		Code: "CHK-JPY", RateBP: 0,
		MonthlyFeeMinor: 300, // JPY is scale 0, so this is 300 yen
		MinBalanceMinor: 0, MinBalanceFeeMinor: 0,
		Currency: "JPY",
	},
	"SUSPENSE": {Code: "SUSPENSE", Currency: "USD"},
}
