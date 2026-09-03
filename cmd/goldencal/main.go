// Command goldencal emits the documented calendar, day counts and rounding as
// JSON. legacy_sim.calendar is tested against this file, so the Go and Python
// halves of LLD §5.4 cannot drift apart silently (risk R7).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/roshanrana/shadowbook/internal/bizdate"
)

type dayFact struct {
	Date       string `json:"date"`
	Weekday    string `json:"weekday"`
	IsBusiness bool   `json:"is_business_day"`
	Holiday    string `json:"holiday,omitempty"`
}

type monthFact struct {
	Year                    int    `json:"year"`
	Month                   int    `json:"month"`
	FirstOfMonth            string `json:"first_of_month"`
	FirstBusinessDayOfMonth string `json:"first_business_day_of_month"`
}

type cutoffFact struct {
	Instant      string `json:"instant"`
	BusinessDate string `json:"business_date"`
}

type dayCountFact struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Basis string `json:"basis"`
	Num   int64  `json:"num"`
	Den   int64  `json:"den"`
}

type roundFact struct {
	Num      int64 `json:"num"`
	Den      int64 `json:"den"`
	HalfEven int64 `json:"half_even"`
	HalfUp   int64 `json:"half_up"`
}

type golden struct {
	Days      []dayFact      `json:"days"`
	Months    []monthFact    `json:"months"`
	Cutoffs   []cutoffFact   `json:"cutoffs"`
	DayCounts []dayCountFact `json:"day_counts"`
	Rounding  []roundFact    `json:"rounding"`
}

func main() {
	c := bizdate.NewUSFederal(time.UTC)
	g := golden{}

	// Every day of 2027-2029: the full holiday and weekend surface.
	for d := bizdate.Date(2027, time.January, 1); !d.After(bizdate.Date(2029, time.December, 31)); d = d.AddDays(1) {
		f := dayFact{Date: d.String(), Weekday: d.Weekday().String(), IsBusiness: c.IsBusinessDay(d)}
		if n, ok := c.HolidayName(d); ok {
			f.Holiday = n
		}
		g.Days = append(g.Days, f)
	}

	for y := 2027; y <= 2029; y++ {
		for m := time.January; m <= time.December; m++ {
			g.Months = append(g.Months, monthFact{
				Year: y, Month: int(m),
				FirstOfMonth:            c.FirstOfMonth(y, m).String(),
				FirstBusinessDayOfMonth: c.FirstBusinessDayOfMonth(y, m).String(),
			})
		}
	}

	// The exclusive 17:00 boundary, on both a plain day and a Friday.
	for _, base := range []bizdate.BusinessDate{
		bizdate.Date(2028, time.March, 1), bizdate.Date(2028, time.March, 31),
		bizdate.Date(2028, time.February, 29), bizdate.Date(2028, time.October, 6),
	} {
		for _, hms := range [][4]int{
			{0, 0, 0, 0}, {16, 59, 59, 999000000}, {17, 0, 0, 0}, {17, 0, 0, 1000000}, {23, 59, 59, 999000000},
		} {
			at := time.Date(base.Y, base.M, base.D, hms[0], hms[1], hms[2], hms[3], time.UTC)
			g.Cutoffs = append(g.Cutoffs, cutoffFact{
				Instant: at.Format(time.RFC3339Nano), BusinessDate: c.BusinessDateFor(at).String()})
		}
	}

	for _, tc := range []struct {
		from, to bizdate.BusinessDate
		b        bizdate.Basis
	}{
		{bizdate.Date(2027, time.March, 1), bizdate.Date(2027, time.March, 2), bizdate.ACT365},
		{bizdate.Date(2028, time.March, 1), bizdate.Date(2028, time.March, 2), bizdate.ACTACT},
		{bizdate.Date(2027, time.March, 1), bizdate.Date(2027, time.March, 2), bizdate.ACTACT},
		{bizdate.Date(2028, time.March, 1), bizdate.Date(2028, time.March, 2), bizdate.ACT360},
		{bizdate.Date(2028, time.February, 28), bizdate.Date(2028, time.March, 1), bizdate.ACTACT},
		{bizdate.Date(2028, time.January, 1), bizdate.Date(2029, time.January, 1), bizdate.ACTACT},
		{bizdate.Date(2028, time.February, 29), bizdate.Date(2028, time.March, 31), bizdate.ACT365},
	} {
		num, den := bizdate.DayCountFraction(tc.from, tc.to, tc.b)
		g.DayCounts = append(g.DayCounts, dayCountFact{
			From: tc.from.String(), To: tc.to.String(), Basis: tc.b.String(), Num: num, Den: den})
	}

	for _, tc := range [][2]int64{
		{5, 2}, {7, 2}, {-5, 2}, {-7, 2}, {1, 3}, {2, 3}, {11, 4}, {9, 4},
		{0, 7}, {-1, 3}, {100, 1}, {12345, 365}, {12345, 366}, {12345, 360},
	} {
		g.Rounding = append(g.Rounding, roundFact{
			Num: tc[0], Den: tc[1],
			HalfEven: bizdate.RoundHalfEven(tc[0], tc[1]),
			HalfUp:   bizdate.RoundHalfUp(tc[0], tc[1]),
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(g); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
