package bizdate

import (
	"testing"
	"time"
)

func cal() *USFederal { return NewUSFederal(time.UTC) }

// The cut-off is EXCLUSIVE at 17:00:00.000 (FR-L12). Q2 is the legacy core
// rolling 16:59:59.999 to the next day, so these three instants are the exact
// boundary Q2 is measured against.
func TestCutOffBoundaryIsExclusive(t *testing.T) {
	c := cal()
	// 2028-03-01 is a Wednesday, a plain business day either side of the cut.
	day := func(h, m, s, ns int) time.Time {
		return time.Date(2028, time.March, 1, h, m, s, ns, time.UTC)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		want BusinessDate
	}{
		{"16:59:59.999 stays today", day(16, 59, 59, 999_000_000), Date(2028, time.March, 1)},
		{"17:00:00.000 rolls (exclusive)", day(17, 0, 0, 0), Date(2028, time.March, 2)},
		{"17:00:00.001 rolls", day(17, 0, 0, 1_000_000), Date(2028, time.March, 2)},
		{"00:00:00.000 stays today", day(0, 0, 0, 0), Date(2028, time.March, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.BusinessDateFor(tc.at); got != tc.want {
				t.Fatalf("BusinessDateFor(%s) = %s, want %s", tc.at.Format(time.RFC3339Nano), got, tc.want)
			}
		})
	}
}

func TestCutOffRollsOverNonBusinessDays(t *testing.T) {
	c := cal()
	// Friday 2028-03-31 after the cut-off: Sat/Sun are not business days, so
	// the next business date is Monday 2028-04-03.
	at := time.Date(2028, time.March, 31, 17, 0, 0, 0, time.UTC)
	if got, want := c.BusinessDateFor(at), Date(2028, time.April, 3); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	// Saturday before the cut-off still rolls: a Saturday has no business date.
	sat := time.Date(2028, time.April, 1, 9, 0, 0, 0, time.UTC)
	if got, want := c.BusinessDateFor(sat), Date(2028, time.April, 3); got != want {
		t.Fatalf("saturday: got %s, want %s", got, want)
	}
}

// The calendar facts D-010's two windows depend on. If any of these change,
// Finding 1 loses a quirk.
func TestD010CalendarFacts(t *testing.T) {
	c := cal()
	for _, tc := range []struct {
		name string
		date BusinessDate
		want bool
	}{
		{"2028-02-29 leap day is a business day (Q6)", Date(2028, time.February, 29), true},
		{"2028-03-01 is a business day (Wednesday)", Date(2028, time.March, 1), true},
		{"2028-04-01 is NOT a business day (Saturday) -- Q12 depends on this",
			Date(2028, time.April, 1), false},
		{"2028-03-31 month end is a business day", Date(2028, time.March, 31), true},
		{"Columbus Day 2028-10-09 is NOT a business day (Q5)",
			Date(2028, time.October, 9), false},
		{"2028-10-10 is a business day", Date(2028, time.October, 10), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.IsBusinessDay(tc.date); got != tc.want {
				t.Fatalf("IsBusinessDay(%s) = %v, want %v", tc.date, got, tc.want)
			}
		})
	}
	if n, ok := c.HolidayName(Date(2028, time.October, 9)); !ok || n != "Columbus Day" {
		t.Fatalf("2028-10-09 holiday = %q, %v; want Columbus Day", n, ok)
	}
}

// D-010's windows, asserted by counting. The plan claims W1 is exactly 30
// business days and W2 exactly 9; if that is wrong the demo window is wrong.
func TestD010WindowLengths(t *testing.T) {
	c := cal()
	w1 := c.BusinessDaysBetween(Date(2028, time.February, 28), Date(2028, time.April, 7))
	if len(w1) != 30 {
		t.Fatalf("W1 2028-02-28..2028-04-07 = %d business days, want 30", len(w1))
	}
	if w1[0] != Date(2028, time.February, 28) || w1[len(w1)-1] != Date(2028, time.April, 7) {
		t.Fatalf("W1 spans %s..%s", w1[0], w1[len(w1)-1])
	}
	w2 := c.BusinessDaysBetween(Date(2028, time.October, 2), Date(2028, time.October, 13))
	if len(w2) != 9 {
		t.Fatalf("W2 2028-10-02..2028-10-13 = %d business days, want 9 (Columbus Day excluded)", len(w2))
	}
	for _, d := range w2 {
		if d == Date(2028, time.October, 9) {
			t.Fatal("W2 contains Columbus Day; it must be excluded by the documented calendar")
		}
	}
	// No federal holiday inside W1 -- Presidents Day 2028-02-21 is outside it.
	for _, d := range w1 {
		if n, ok := c.HolidayName(d); ok {
			t.Fatalf("unexpected holiday %q inside W1 on %s", n, d)
		}
	}
}

// Q12 is only detectable when the calendar first differs from the first
// business day. April 2028 is the month in W1 where that holds.
func TestFirstOfMonthVsFirstBusinessDay(t *testing.T) {
	c := cal()
	for _, tc := range []struct {
		y            int
		m            time.Month
		wantCal      BusinessDate
		wantBusiness BusinessDate
		differ       bool
	}{
		{2028, time.April, Date(2028, time.April, 1), Date(2028, time.April, 3), true},
		{2028, time.March, Date(2028, time.March, 1), Date(2028, time.March, 1), false},
		{2028, time.January, Date(2028, time.January, 1), Date(2028, time.January, 3), true},
	} {
		got, gotB := c.FirstOfMonth(tc.y, tc.m), c.FirstBusinessDayOfMonth(tc.y, tc.m)
		if got != tc.wantCal || gotB != tc.wantBusiness {
			t.Fatalf("%s %d: cal=%s bus=%s; want %s / %s", tc.m, tc.y, got, gotB, tc.wantCal, tc.wantBusiness)
		}
		if (got != gotB) != tc.differ {
			t.Fatalf("%s %d: differ=%v, want %v", tc.m, tc.y, got != gotB, tc.differ)
		}
	}
}

func TestHolidayObservedShift(t *testing.T) {
	c := cal()
	// 2027-07-04 is a Sunday -> observed Monday 2027-07-05.
	if c.IsBusinessDay(Date(2027, time.July, 5)) {
		t.Fatal("2027-07-05 should be the observed Independence Day")
	}
	// 2027-12-25 is a Saturday -> observed Friday 2027-12-24.
	if c.IsBusinessDay(Date(2027, time.December, 24)) {
		t.Fatal("2027-12-24 should be the observed Christmas Day")
	}
	// Memorial Day is the LAST Monday in May: 2028-05-29.
	if c.IsBusinessDay(Date(2028, time.May, 29)) {
		t.Fatal("2028-05-29 should be Memorial Day")
	}
	if !c.IsBusinessDay(Date(2028, time.May, 22)) {
		t.Fatal("2028-05-22 is the fourth Monday, not the last -- must be a business day")
	}
}

func TestLeapYearAndDaysInYear(t *testing.T) {
	for _, tc := range []struct {
		y    int
		leap bool
		days int64
	}{
		{2027, false, 365}, {2028, true, 366}, {2000, true, 366}, {1900, false, 365}, {2100, false, 365},
	} {
		if IsLeapYear(tc.y) != tc.leap || DaysInYear(tc.y) != tc.days {
			t.Fatalf("%d: leap=%v days=%d; want %v/%d", tc.y, IsLeapYear(tc.y), DaysInYear(tc.y), tc.leap, tc.days)
		}
	}
}

// FR-L6: ACT/365 normally, ACT/ACT in a leap year. Q3 and Q6 diverge from these.
func TestDayCountFraction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to BusinessDate
		basis    Basis
		num, den int64
	}{
		{"one day ACT/365", Date(2027, time.March, 1), Date(2027, time.March, 2), ACT365, 1, 365},
		{"one day ACT/ACT in 2028 uses 366", Date(2028, time.March, 1), Date(2028, time.March, 2), ACTACT, 1, 366},
		{"ACT/ACT in a non-leap year is ACT/365", Date(2027, time.March, 1), Date(2027, time.March, 2), ACTACT, 1, 365},
		{"Q3's ACT/360", Date(2028, time.March, 1), Date(2028, time.March, 2), ACT360, 1, 360},
		{"across the leap day", Date(2028, time.February, 28), Date(2028, time.March, 1), ACTACT, 2, 366},
		{"a full leap year", Date(2028, time.January, 1), Date(2029, time.January, 1), ACTACT, 366, 366},
	} {
		t.Run(tc.name, func(t *testing.T) {
			num, den := DayCountFraction(tc.from, tc.to, tc.basis)
			if num != tc.num || den != tc.den {
				t.Fatalf("= %d/%d, want %d/%d", num, den, tc.num, tc.den)
			}
		})
	}
}

func TestBasisFor(t *testing.T) {
	if got := BasisFor(Date(2028, time.June, 1)); got != ACTACT {
		t.Fatalf("2028 basis = %v, want ACT/ACT", got)
	}
	if got := BasisFor(Date(2027, time.June, 1)); got != ACT365 {
		t.Fatalf("2027 basis = %v, want ACT/365", got)
	}
}

// Half-even is the documented rule; Q1 is half-up. The two differ only on exact
// ties, which is why a Q1 break is at most one minor unit.
func TestRoundHalfEven(t *testing.T) {
	for _, tc := range []struct {
		num, den, want int64
	}{
		{5, 2, 2},   // 2.5 -> 2 (down to even)
		{7, 2, 4},   // 3.5 -> 4 (up to even)
		{-5, 2, -2}, // -2.5 -> -2
		{-7, 2, -4}, // -3.5 -> -4
		{1, 3, 0},   // 0.33
		{2, 3, 1},   // 0.67
		{4, 2, 2},   // exact
		{0, 7, 0},   //
		{-1, 3, 0},  //
		{11, 4, 3},  // 2.75
		{9, 4, 2},   // 2.25
		{5, -2, -2}, // negative denominator normalised
		{100, 1, 100},
	} {
		if got := RoundHalfEven(tc.num, tc.den); got != tc.want {
			t.Fatalf("RoundHalfEven(%d,%d) = %d, want %d", tc.num, tc.den, got, tc.want)
		}
	}
}

// Q1's rule, kept here only so the simulator and reconciler can name it.
func TestRoundHalfUpDiffersOnlyOnTies(t *testing.T) {
	ties := [][2]int64{{5, 2}, {-5, 2}, {7, 2}, {-7, 2}}
	for _, tc := range ties {
		he, hu := RoundHalfEven(tc[0], tc[1]), RoundHalfUp(tc[0], tc[1])
		if he == hu && tc[0] != 7 && tc[0] != -7 {
			t.Fatalf("half-even and half-up agree on the tie %d/%d (%d) -- Q1 would be invisible",
				tc[0], tc[1], he)
		}
	}
	// Away from a tie they must agree, or the break would not be Q1's signature.
	for _, tc := range [][2]int64{{1, 3}, {2, 3}, {11, 4}, {9, 4}, {-1, 3}} {
		if RoundHalfEven(tc[0], tc[1]) != RoundHalfUp(tc[0], tc[1]) {
			t.Fatalf("non-tie %d/%d disagrees: %d vs %d",
				tc[0], tc[1], RoundHalfEven(tc[0], tc[1]), RoundHalfUp(tc[0], tc[1]))
		}
	}
}

func TestRoundPanicsOnZeroDenominator(t *testing.T) {
	for _, f := range []func(int64, int64) int64{RoundHalfEven, RoundHalfUp} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("zero denominator must panic rather than return a wrong number")
				}
			}()
			_ = f(1, 0)
		}()
	}
}

func TestDateParseStringRoundTrip(t *testing.T) {
	d, err := Parse("2028-02-29")
	if err != nil || d != Date(2028, time.February, 29) {
		t.Fatalf("Parse = %v, %v", d, err)
	}
	if d.String() != "2028-02-29" {
		t.Fatalf("String = %q", d.String())
	}
	if _, err := Parse("2027-02-29"); err == nil {
		t.Fatal("2027-02-29 is not a real date and must not parse")
	}
	if _, err := Parse("not-a-date"); err == nil {
		t.Fatal("garbage must not parse")
	}
}

func TestNextPrevBusinessDay(t *testing.T) {
	c := cal()
	// Friday 2028-03-31 -> Monday 2028-04-03 (Apr 1 is a Saturday).
	if got, want := c.NextBusinessDay(Date(2028, time.March, 31)), Date(2028, time.April, 3); got != want {
		t.Fatalf("next = %s, want %s", got, want)
	}
	if got, want := c.PrevBusinessDay(Date(2028, time.April, 3)), Date(2028, time.March, 31); got != want {
		t.Fatalf("prev = %s, want %s", got, want)
	}
	// Across Columbus Day: Friday 2028-10-06 -> Tuesday 2028-10-10.
	if got, want := c.NextBusinessDay(Date(2028, time.October, 6)), Date(2028, time.October, 10); got != want {
		t.Fatalf("across Columbus Day: next = %s, want %s", got, want)
	}
}

func TestDaysBetweenAndAddDays(t *testing.T) {
	if got := Date(2028, time.February, 28).DaysBetween(Date(2028, time.March, 1)); got != 2 {
		t.Fatalf("Feb 28 -> Mar 1 in a leap year = %d days, want 2", got)
	}
	if got := Date(2027, time.February, 28).DaysBetween(Date(2027, time.March, 1)); got != 1 {
		t.Fatalf("non-leap = %d days, want 1", got)
	}
	if got := Date(2028, time.February, 28).AddDays(1); got != Date(2028, time.February, 29) {
		t.Fatalf("AddDays across the leap day = %s", got)
	}
	a, b := Date(2028, time.March, 1), Date(2028, time.March, 2)
	if !a.Before(b) || !b.After(a) || !a.Equal(Date(2028, time.March, 1)) {
		t.Fatal("comparison helpers are wrong")
	}
}

func TestBasisString(t *testing.T) {
	for b, want := range map[Basis]string{ACT365: "ACT/365", ACTACT: "ACT/ACT", ACT360: "ACT/360", Basis(9): "Basis(9)"} {
		if got := b.String(); got != want {
			t.Fatalf("Basis(%d).String() = %q, want %q", uint8(b), got, want)
		}
	}
}
