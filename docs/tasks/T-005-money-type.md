# T-005 — `internal/money`: Amount, currency registry, scale
Status: todo      Milestone: M0   Wave: 3
Depends on: T-002

## Goal
One money type for the whole Go side. Scale comes from a compile-time registry keyed by currency, never from the caller, so there is no code path that can store JPY at scale 2.

## Context
- FR-L11 and LLD §3.1. This is the structural defence against Q10 (`jpy_two_decimals_truncated`): the legacy core stores JPY at scale 2 and truncates; the shadow must be *incapable* of it, not merely tested against it.
- CLAUDE.md: "Money is never a float." No `float64`, no `string` arithmetic, anywhere.
- LLD §2: a bare `int64` amount crossing a package boundary is a review failure.

## Contracts to honor
Frozen, LLD §3.1:
```go
type Currency string

type Amount struct {
    Minor    int64
    Currency Currency
    Scale    uint8
}

func New(minor int64, c Currency) (Amount, error)
func (a Amount) Add(b Amount) (Amount, error)
func (a Amount) Neg() Amount
func (a Amount) IsZero() bool
```
Registry: `USD→2`, `EUR→2`, `JPY→0`. `New` rejects an unknown currency.

## File scope
Create: `internal/money/money.go`, `internal/money/registry.go`, `internal/money/money_test.go`
Modify: —

## Suggested steps
1. Registry as an unexported `map[Currency]uint8` with an accessor; no exported mutation.
2. `New` returns `ErrUnknownCurrency` for anything not in the registry.
3. `Add` returns `ErrCurrencyMismatch` when currencies differ. Overflow on `int64` addition must return an error, not wrap silently.
4. Do **not** add `Mul`, `Div`, `String` or JSON marshalling — later tasks will ask; they are out of scope and rounding belongs in `bizdate` (T-006).
5. Table-driven tests including every negative case.

## Acceptance criteria
- [ ] `New(100, "JPY")` yields `Scale == 0`; there is no exported way to construct a JPY Amount with `Scale == 2`
- [ ] `New(1, "XXX")` returns `ErrUnknownCurrency`
- [ ] `Add` across currencies returns `ErrCurrencyMismatch`
- [ ] Addition that would overflow `int64` returns an error and does not wrap
- [ ] `Neg()` of `math.MinInt64` is handled explicitly (it has no positive counterpart) — test it
- [ ] `grep -rn "float" internal/money/` returns nothing
- [ ] Coverage of this package ≥ 95%

## Validation
```
go test -race ./internal/money/...
make check
```

## Out of scope
Multiplication and division (accrual does its own integer arithmetic in T-021). Formatting or parsing. JSON tags. Protobuf conversion (T-007).

## Handoff notes
_(filled by the worker)_
