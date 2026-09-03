// Package bizdate holds the shadow ledger's documented calendar rules: business
// days and holidays, the cut-off, day-count bases and rounding.
//
// This package is the Go half of LLD §5.4. Six of the twelve seeded quirks
// diverge from exactly these rules -- Q1 rounding, Q2 cut-off, Q3 and Q6 day
// count, Q5 Columbus Day, Q12 posting date -- so an error here does not produce
// a test failure, it produces a wrong Finding 1. legacy_sim.calendar is the
// Python mirror and is golden-tested against this package.
//
// Nothing here reads the wall clock and nothing here uses float.
package bizdate

import (
	"fmt"
	"time"
)

// BusinessDate is a civil date. It is deliberately not a time.Time: a business
// date has no instant, no zone and no hour (LLD §2).
type BusinessDate struct {
	Y int
	M time.Month
	D int
}

// Date builds a BusinessDate, normalising as time.Date would.
func Date(y int, m time.Month, d int) BusinessDate {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return BusinessDate{Y: t.Year(), M: t.Month(), D: t.Day()}
}

func (b BusinessDate) time() time.Time {
	return time.Date(b.Y, b.M, b.D, 0, 0, 0, 0, time.UTC)
}

// String renders ISO 8601, which is also the extract and API wire format.
func (b BusinessDate) String() string { return fmt.Sprintf("%04d-%02d-%02d", b.Y, b.M, b.D) }

// Parse reads an ISO 8601 date.
func Parse(s string) (BusinessDate, error) {
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return BusinessDate{}, fmt.Errorf("bizdate: parse %q: %w", s, err)
	}
	return BusinessDate{Y: t.Year(), M: t.Month(), D: t.Day()}, nil
}

// AddDays moves by calendar days, not business days.
func (b BusinessDate) AddDays(n int) BusinessDate {
	t := b.time().AddDate(0, 0, n)
	return BusinessDate{Y: t.Year(), M: t.Month(), D: t.Day()}
}

func (b BusinessDate) Before(o BusinessDate) bool { return b.time().Before(o.time()) }
func (b BusinessDate) After(o BusinessDate) bool  { return b.time().After(o.time()) }
func (b BusinessDate) Equal(o BusinessDate) bool  { return b == o }

// Weekday is the day of week.
func (b BusinessDate) Weekday() time.Weekday { return b.time().Weekday() }

// DaysBetween returns the count of calendar days from b to o (o - b).
func (b BusinessDate) DaysBetween(o BusinessDate) int64 {
	return int64(o.time().Sub(b.time()) / (24 * time.Hour))
}

// IsLeapYear reports whether y is a leap year. ACT/ACT depends on it (Q6).
func IsLeapYear(y int) bool { return (y%4 == 0 && y%100 != 0) || y%400 == 0 }

// DaysInYear is 366 in a leap year, else 365.
func DaysInYear(y int) int64 {
	if IsLeapYear(y) {
		return 366
	}
	return 365
}
