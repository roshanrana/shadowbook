package bizdate

import "time"

// Holiday rules as data rather than code, evaluated per year.
//
// Columbus Day is a HOLIDAY in the documented calendar. Q5
// (columbus_day_is_business_day) is the legacy core treating it as a business
// day; if this entry is ever removed, Q5 silently stops being detectable and
// Finding 1 under-reports without failing.
type holidayRule struct {
	name  string
	month time.Month
	// Exactly one form is used, selected by fixed.
	day   int          // fixed-date form; observed-shifted on a weekend fall
	nth   int          // nth-weekday form; -1 means "last"
	wd    time.Weekday // nth-weekday form
	fixed bool
}

var federalHolidays = []holidayRule{
	{name: "New Year's Day", month: time.January, day: 1, fixed: true},
	{name: "Martin Luther King Jr. Day", month: time.January, nth: 3, wd: time.Monday},
	{name: "Washington's Birthday", month: time.February, nth: 3, wd: time.Monday},
	{name: "Memorial Day", month: time.May, nth: -1, wd: time.Monday},
	{name: "Juneteenth", month: time.June, day: 19, fixed: true},
	{name: "Independence Day", month: time.July, day: 4, fixed: true},
	{name: "Labor Day", month: time.September, nth: 1, wd: time.Monday},
	{name: "Columbus Day", month: time.October, nth: 2, wd: time.Monday},
	{name: "Veterans Day", month: time.November, day: 11, fixed: true},
	{name: "Thanksgiving", month: time.November, nth: 4, wd: time.Thursday},
	{name: "Christmas Day", month: time.December, day: 25, fixed: true},
}

// nthWeekday returns the nth wd of month m in year y. n == -1 means the last.
func nthWeekday(y int, m time.Month, wd time.Weekday, n int) BusinessDate {
	if n == -1 {
		d := Date(y, m, daysInMonth(y, m))
		for d.Weekday() != wd {
			d = d.AddDays(-1)
		}
		return d
	}
	d := Date(y, m, 1)
	for d.Weekday() != wd {
		d = d.AddDays(1)
	}
	return d.AddDays(7 * (n - 1))
}

func daysInMonth(y int, m time.Month) int {
	return Date(y, m+1, 1).AddDays(-1).D
}

// observed shifts a fixed-date holiday off a weekend: Saturday is observed on
// the preceding Friday, Sunday on the following Monday.
func observed(d BusinessDate) BusinessDate {
	switch d.Weekday() {
	case time.Saturday:
		return d.AddDays(-1)
	case time.Sunday:
		return d.AddDays(1)
	default:
		return d
	}
}

// holidaysIn returns the observed holiday dates for a year.
func holidaysIn(y int) map[BusinessDate]string {
	out := make(map[BusinessDate]string, len(federalHolidays))
	for _, r := range federalHolidays {
		var d BusinessDate
		if r.fixed {
			d = observed(Date(y, r.month, r.day))
		} else {
			d = nthWeekday(y, r.month, r.wd, r.nth)
		}
		out[d] = r.name
	}
	return out
}
