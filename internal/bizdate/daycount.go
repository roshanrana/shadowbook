package bizdate

import "fmt"

// Basis is a day-count convention.
type Basis uint8

const (
	// ACT365 is the documented basis for all products in a normal year
	// (FR-L6). Q3 seeds the legacy core using ACT/360 on product SAV-01.
	ACT365 Basis = iota
	// ACTACT is the documented basis in a leap year: actual days over the
	// actual length of the year, so 366 in 2028. Q6 seeds the legacy core
	// staying on ACT/365 through a leap year, which over-accrues on day 366.
	ACTACT
	// ACT360 is NOT a documented shadow basis. It exists only so the
	// simulator and the reconciler's model-rule table can name what Q3 does.
	ACT360
)

func (b Basis) String() string {
	switch b {
	case ACT365:
		return "ACT/365"
	case ACTACT:
		return "ACT/ACT"
	case ACT360:
		return "ACT/360"
	default:
		return fmt.Sprintf("Basis(%d)", uint8(b))
	}
}

// BasisFor returns the documented basis for a period starting on d: ACT/ACT in
// a leap year, ACT/365 otherwise. This one function is the whole of FR-L6's
// "ACT/365, ACT/ACT in leap years" rule; nothing else may decide it.
func BasisFor(d BusinessDate) Basis {
	if IsLeapYear(d.Y) {
		return ACTACT
	}
	return ACT365
}

// DayCountFraction returns the accrual fraction from..to as an exact integer
// ratio (num, den). It never returns a float: the caller multiplies by the
// principal and the rate and then rounds once, so no intermediate rounding can
// creep in.
//
// num is the count of actual calendar days in (from, to]. den is the basis
// denominator, taken from the year of `from` when the basis is ACT/ACT.
func DayCountFraction(from, to BusinessDate, b Basis) (num, den int64) {
	num = from.DaysBetween(to)
	switch b {
	case ACT365:
		den = 365
	case ACTACT:
		den = DaysInYear(from.Y)
	case ACT360:
		den = 360
	default:
		den = 365
	}
	return num, den
}

// RoundHalfEven rounds num/den to the nearest integer, ties to even. This is
// the documented rounding rule (FR-L6); Q1 seeds the legacy core rounding half
// away from zero, so a break of at most one minor unit is Q1's signature.
//
// Integer arithmetic throughout. den must be non-zero.
func RoundHalfEven(num, den int64) int64 {
	if den == 0 {
		panic("bizdate: RoundHalfEven with zero denominator")
	}
	if den < 0 {
		num, den = -num, -den
	}

	neg := num < 0
	if neg {
		num = -num
	}

	q, r := num/den, num%den
	// Compare 2r against den to find which side of the halfway point we are on.
	switch double := 2 * r; {
	case double > den:
		q++
	case double == den:
		if q%2 != 0 { // tie: round to the even quotient
			q++
		}
	}

	if neg {
		return -q
	}
	return q
}

// RoundHalfUp rounds num/den half away from zero. NOT a documented shadow rule
// -- it exists so the reconciler's model-rule table and the simulator can name
// what Q1 does. Never call it from a posting path.
func RoundHalfUp(num, den int64) int64 {
	if den == 0 {
		panic("bizdate: RoundHalfUp with zero denominator")
	}
	if den < 0 {
		num, den = -num, -den
	}
	neg := num < 0
	if neg {
		num = -num
	}
	q, r := num/den, num%den
	if 2*r >= den {
		q++
	}
	if neg {
		return -q
	}
	return q
}
