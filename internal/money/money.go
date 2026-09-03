// Package money is the single money representation for the Go side of
// SHADOWBOOK. Amounts are integer minor units with an explicit currency and a
// scale that is looked up from a compile-time registry -- never supplied by the
// caller (FR-L11, LLD §3.1).
//
// That last point is the whole design. Q10 seeds a legacy core that stores JPY
// at scale 2 and truncates; the shadow must be structurally incapable of the
// same mistake, not merely tested against it. There is no exported path that
// constructs a JPY Amount with Scale 2.
//
// No float appears in this package, or anywhere downstream of it.
package money

import (
	"errors"
	"fmt"
	"math"
)

// Currency is an ISO 4217 alphabetic code, upper-case.
type Currency string

// Amount is a signed quantity of money in minor units.
//
// Scale is populated by New from the registry and is not a caller input. Two
// Amounts of the same Currency always carry the same Scale.
type Amount struct {
	Minor    int64
	Currency Currency
	Scale    uint8
}

var (
	// ErrUnknownCurrency is returned for any code absent from the registry.
	ErrUnknownCurrency = errors.New("money: unknown currency")
	// ErrCurrencyMismatch is returned by arithmetic across two currencies.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	// ErrOverflow is returned when a result will not fit in int64. It is never
	// wrapped silently: a wrapped balance is a broken ledger.
	ErrOverflow = errors.New("money: int64 overflow")
	// ErrUnrepresentable is returned by New for math.MinInt64, which has no
	// positive counterpart and would make Neg partial. Rejecting it here keeps
	// Neg total, which is why Neg can return an Amount rather than an error.
	ErrUnrepresentable = errors.New("money: amount is unrepresentable")
)

// New builds an Amount, taking Scale from the registry.
func New(minor int64, c Currency) (Amount, error) {
	scale, ok := scaleOf(c)
	if !ok {
		return Amount{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, string(c))
	}
	if minor == math.MinInt64 {
		return Amount{}, fmt.Errorf("%w: %d", ErrUnrepresentable, minor)
	}
	return Amount{Minor: minor, Currency: c, Scale: scale}, nil
}

// Zero is the zero Amount in c.
func Zero(c Currency) (Amount, error) { return New(0, c) }

// Add returns a+b. Currencies must match and the result must fit in int64.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.Currency != b.Currency {
		return Amount{}, fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, a.Currency, b.Currency)
	}
	sum := a.Minor + b.Minor
	// Overflow iff the operands share a sign and the result does not.
	if (a.Minor > 0 && b.Minor > 0 && sum <= 0) || (a.Minor < 0 && b.Minor < 0 && sum >= 0) {
		return Amount{}, fmt.Errorf("%w: %d + %d", ErrOverflow, a.Minor, b.Minor)
	}
	return Amount{Minor: sum, Currency: a.Currency, Scale: a.Scale}, nil
}

// Sub returns a-b.
func (a Amount) Sub(b Amount) (Amount, error) { return a.Add(b.Neg()) }

// Neg returns -a. Total, because New rejects math.MinInt64.
func (a Amount) Neg() Amount {
	return Amount{Minor: -a.Minor, Currency: a.Currency, Scale: a.Scale}
}

// IsZero reports whether the amount is exactly zero.
func (a Amount) IsZero() bool { return a.Minor == 0 }

// Sum adds amounts left to right. An empty sum is an error rather than a
// guessed zero, because the currency would have to be invented.
func Sum(amounts ...Amount) (Amount, error) {
	if len(amounts) == 0 {
		return Amount{}, errors.New("money: Sum of no amounts")
	}
	acc := amounts[0]
	for _, x := range amounts[1:] {
		var err error
		if acc, err = acc.Add(x); err != nil {
			return Amount{}, err
		}
	}
	return acc, nil
}

// String renders for logs and errors only. It is deliberately not a formatter
// for presentation and does no locale work.
func (a Amount) String() string {
	return fmt.Sprintf("%d %s(scale %d)", a.Minor, a.Currency, a.Scale)
}
