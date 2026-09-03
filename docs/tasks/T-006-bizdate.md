# T-006 — `internal/bizdate`: calendar, cut-off, day count, rounding
Status: todo      Milestone: M0   Wave: 3
Depends on: T-002

## Goal
The single place in Go where the shadow's documented calendar rules live: business days and holidays, the 17:00 exclusive cut-off, ACT/365 and ACT/ACT day counts, and half-even rounding. Six of the twelve quirks diverge from exactly these rules, so an error here silently corrupts Finding 1.

## Context
- LLD §3.2 and §5.4. The §5.4 table is the shadow side of the `documented:` fields in `legacy-sim/quirks.yaml` — read that file.
- Q1 diverges on rounding, Q2 on cut-off, Q3 and Q6 on day count, Q5 on Columbus Day, Q12 on posting date.
- `BusinessDate` is a civil date and is **never** a `time.Time` (LLD §2). Wall-clock instants come only from an injected clock.
- Columbus Day is a **holiday** in the documented calendar. Q5 is the legacy core treating it as a business day.
- D-010 fixes the windows this must get right: 2028-02-28 → 2028-04-07, and 2028-10-02 → 2028-10-13.

## Contracts to honor
Frozen, LLD §3.2 — `BusinessDate`, the `Calendar` interface, `BusinessDateFor`, `Basis`, `DayCountFraction`, `RoundHalfEven`. Holiday table covers 2027–2029 with the observed-day rule for weekend falls.

## File scope
Create: `internal/bizdate/date.go`, `internal/bizdate/calendar.go`, `internal/bizdate/holidays.go`, `internal/bizdate/daycount.go`, `internal/bizdate/rounding.go`, and `_test.go` for each.
Modify: —

## Suggested steps
1. `BusinessDate` as a comparable struct with `Before`/`After`/`AddDays`; no timezone anywhere in it.
2. Holiday table as data, not code: a slice of rules (fixed date, nth weekday) evaluated per year, with the observed-day shift for Saturday/Sunday falls.
3. `BusinessDateFor(t)`: `t` before 17:00:00.000 → that day's business date; at or after → next business day. **Exclusive** boundary — test 16:59:59.999, 17:00:00.000 and 17:00:00.001.
4. `DayCountFraction` returns `(num, den)` integers; the caller does the integer arithmetic. Never return a float.
5. `RoundHalfEven(numerator, denominator)` on integers only; ties go to even.

## Acceptance criteria
- [ ] Cut-off boundary tested at 16:59:59.999 / 17:00:00.000 / 17:00:00.001
- [ ] 2028-02-29 is a business day (Tuesday); 2028-04-01 is **not** (Saturday); 2028-03-01 **is** (Wednesday)
- [ ] Columbus Day 2028-10-09 is **not** a business day
- [ ] `FirstOfMonth(2028, April)` and `FirstBusinessDayOfMonth(2028, April)` differ — this is what makes Q12 detectable
- [ ] ACT/ACT in 2028 uses denominator 366; ACT/365 uses 365
- [ ] Half-even ties tested in both directions (2.5→2, 3.5→4 in minor-unit terms)
- [ ] A test asserts W1 (2028-02-28 → 2028-04-07) contains exactly 30 business days and W2 (2028-10-02 → 2028-10-13) exactly 9
- [ ] `grep -rn "float\|time.Now" internal/bizdate/` returns nothing
- [ ] Coverage ≥ 95%

## Validation
```
go test -race ./internal/bizdate/...
make check
```

## Out of scope
Interest calculation itself (T-021). The Python mirror (T-010). Any quirk implementation — this package only knows documented behaviour.

## Handoff notes
_(filled by the worker)_
