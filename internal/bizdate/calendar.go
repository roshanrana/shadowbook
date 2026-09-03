package bizdate

import "time"

// CutOff is the documented daily cut-off: 17:00:00.000, EXCLUSIVE.
//
// An instant strictly before the cut-off belongs to that day's business date;
// an instant at or after it rolls to the next business day. Q2
// (cutoff_inclusive_boundary) is the legacy core treating 16:59:59.999 as
// already belonging to the next day, so the boundary handling here is the thing
// Q2 is measured against.
var CutOff = struct {
	Hour, Minute, Second, Nanosecond int
}{Hour: 17, Minute: 0, Second: 0, Nanosecond: 0}

// Calendar answers business-day questions for the documented calendar.
//
// LLD §3.2 wrote BusinessDateFor as a method "on Calendar"; an interface cannot
// carry a method body, so it is a member of the interface here. No semantic
// change -- see the T-006 handoff note.
type Calendar interface {
	IsBusinessDay(BusinessDate) bool
	NextBusinessDay(BusinessDate) BusinessDate
	PrevBusinessDay(BusinessDate) BusinessDate
	FirstOfMonth(y int, m time.Month) BusinessDate
	FirstBusinessDayOfMonth(y int, m time.Month) BusinessDate
	BusinessDateFor(t time.Time) BusinessDate
	BusinessDaysBetween(from, to BusinessDate) []BusinessDate
	HolidayName(BusinessDate) (string, bool)
}

// USFederal is the documented calendar: weekdays excluding US federal holidays,
// including Columbus Day.
type USFederal struct {
	// loc is the zone the cut-off is evaluated in. UTC unless set.
	loc *time.Location
	// cache of holiday sets by year; built lazily, never mutated after write.
	cache map[int]map[BusinessDate]string
}

// NewUSFederal builds the documented calendar. Passing nil uses UTC.
func NewUSFederal(loc *time.Location) *USFederal {
	if loc == nil {
		loc = time.UTC
	}
	return &USFederal{loc: loc, cache: make(map[int]map[BusinessDate]string)}
}

func (c *USFederal) holidays(y int) map[BusinessDate]string {
	if h, ok := c.cache[y]; ok {
		return h
	}
	h := holidaysIn(y)
	c.cache[y] = h
	return h
}

// HolidayName returns the holiday falling on d, if any.
func (c *USFederal) HolidayName(d BusinessDate) (string, bool) {
	n, ok := c.holidays(d.Y)[d]
	return n, ok
}

// IsBusinessDay reports whether d is a weekday that is not a holiday.
func (c *USFederal) IsBusinessDay(d BusinessDate) bool {
	switch d.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	_, isHoliday := c.holidays(d.Y)[d]
	return !isHoliday
}

// NextBusinessDay returns the first business day strictly after d.
func (c *USFederal) NextBusinessDay(d BusinessDate) BusinessDate {
	for n := d.AddDays(1); ; n = n.AddDays(1) {
		if c.IsBusinessDay(n) {
			return n
		}
	}
}

// PrevBusinessDay returns the last business day strictly before d.
func (c *USFederal) PrevBusinessDay(d BusinessDate) BusinessDate {
	for p := d.AddDays(-1); ; p = p.AddDays(-1) {
		if c.IsBusinessDay(p) {
			return p
		}
	}
}

// FirstOfMonth is the CALENDAR first of the month -- the documented interest
// posting date (FR-L6). Q12 posts on FirstBusinessDayOfMonth instead, so these
// two differing is exactly what makes Q12 detectable.
func (c *USFederal) FirstOfMonth(y int, m time.Month) BusinessDate {
	return Date(y, m, 1)
}

// FirstBusinessDayOfMonth is the first business day on or after the first.
func (c *USFederal) FirstBusinessDayOfMonth(y int, m time.Month) BusinessDate {
	d := Date(y, m, 1)
	if c.IsBusinessDay(d) {
		return d
	}
	return c.NextBusinessDay(d)
}

// BusinessDateFor maps a wall-clock instant to its business date using the
// exclusive 17:00 cut-off. Instants at or after the cut-off, and instants on a
// non-business day, roll forward to the next business day.
func (c *USFederal) BusinessDateFor(t time.Time) BusinessDate {
	lt := t.In(c.loc)
	d := BusinessDate{Y: lt.Year(), M: lt.Month(), D: lt.Day()}

	cut := time.Date(lt.Year(), lt.Month(), lt.Day(),
		CutOff.Hour, CutOff.Minute, CutOff.Second, CutOff.Nanosecond, c.loc)

	// EXCLUSIVE: at the cut-off exactly, the instant belongs to the next day.
	if !lt.Before(cut) {
		return c.NextBusinessDay(d)
	}
	if !c.IsBusinessDay(d) {
		return c.NextBusinessDay(d)
	}
	return d
}

// BusinessDaysBetween returns every business day in [from, to] inclusive.
func (c *USFederal) BusinessDaysBetween(from, to BusinessDate) []BusinessDate {
	var out []BusinessDate
	for d := from; !d.After(to); d = d.AddDays(1) {
		if c.IsBusinessDay(d) {
			out = append(out, d)
		}
	}
	return out
}

var _ Calendar = (*USFederal)(nil)
