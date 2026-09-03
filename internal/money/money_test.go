package money

import (
	"errors"
	"math"
	"sort"
	"testing"
)

func TestNew(t *testing.T) {
	for _, tc := range []struct {
		name      string
		minor     int64
		currency  Currency
		wantScale uint8
		wantErr   error
	}{
		{"usd two decimals", 125000, "USD", 2, nil},
		{"eur two decimals", -1, "EUR", 2, nil},
		{"jpy is scale zero", 100, "JPY", 0, nil},
		{"jpy zero", 0, "JPY", 0, nil},
		{"unknown currency", 1, "XXX", 0, ErrUnknownCurrency},
		{"lower case is not normalised", 1, "usd", 0, ErrUnknownCurrency},
		{"empty currency", 1, "", 0, ErrUnknownCurrency},
		{"min int64 rejected", math.MinInt64, "USD", 0, ErrUnrepresentable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(tc.minor, tc.currency)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if got.Minor != tc.minor || got.Currency != tc.currency || got.Scale != tc.wantScale {
				t.Fatalf("got %+v, want minor=%d currency=%s scale=%d",
					got, tc.minor, tc.currency, tc.wantScale)
			}
		})
	}
}

// The structural defence against Q10: no exported path yields JPY at scale 2.
func TestJPYCannotBeScaleTwo(t *testing.T) {
	for _, minor := range []int64{0, 1, -1, 999, math.MaxInt64} {
		a, err := New(minor, "JPY")
		if err != nil {
			t.Fatalf("New(%d, JPY): %v", minor, err)
		}
		if a.Scale != 0 {
			t.Fatalf("JPY scale = %d, want 0 -- Q10 is reproducible on the shadow side", a.Scale)
		}
	}
	if s, ok := scaleOf("JPY"); !ok || s != 0 {
		t.Fatalf("registry JPY scale = %d, ok=%v", s, ok)
	}
}

func TestAdd(t *testing.T) {
	usd := func(v int64) Amount { a, _ := New(v, "USD"); return a }
	jpy := func(v int64) Amount { a, _ := New(v, "JPY"); return a }

	for _, tc := range []struct {
		name    string
		a, b    Amount
		want    int64
		wantErr error
	}{
		{"simple", usd(100), usd(23), 123, nil},
		{"to zero", usd(-125000), usd(125000), 0, nil},
		{"negatives", usd(-5), usd(-7), -12, nil},
		{"mismatch", usd(1), jpy(1), 0, ErrCurrencyMismatch},
		{"overflow positive", usd(math.MaxInt64), usd(1), 0, ErrOverflow},
		{"overflow negative", usd(math.MinInt64 + 1), usd(-2), 0, ErrOverflow},
		{"no false overflow at max", usd(math.MaxInt64), usd(0), math.MaxInt64, nil},
		{"opposite signs never overflow", usd(math.MaxInt64), usd(math.MinInt64 + 1), 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Add(tc.b)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got.Minor != tc.want {
				t.Fatalf("minor = %d, want %d", got.Minor, tc.want)
			}
		})
	}
}

func TestSubAndNeg(t *testing.T) {
	a, _ := New(500, "USD")
	b, _ := New(125, "USD")
	d, err := a.Sub(b)
	if err != nil || d.Minor != 375 {
		t.Fatalf("Sub = %v, %v; want 375", d, err)
	}
	if n := a.Neg(); n.Minor != -500 || n.Currency != "USD" || n.Scale != 2 {
		t.Fatalf("Neg = %+v", n)
	}
	// Neg is total precisely because New refuses math.MinInt64.
	if nn := a.Neg().Neg(); nn.Minor != a.Minor {
		t.Fatalf("Neg twice = %d, want %d", nn.Minor, a.Minor)
	}
	if _, err := New(math.MinInt64, "USD"); !errors.Is(err, ErrUnrepresentable) {
		t.Fatal("New must reject MinInt64 or Neg becomes partial")
	}
}

func TestSum(t *testing.T) {
	usd := func(v int64) Amount { a, _ := New(v, "USD"); return a }
	got, err := Sum(usd(-125000), usd(100000), usd(25000))
	if err != nil || got.Minor != 0 {
		t.Fatalf("Sum = %v, %v; want 0 (a balanced posting)", got, err)
	}
	if _, err := Sum(); err == nil {
		t.Fatal("Sum of nothing must error rather than invent a currency")
	}
	jpyA, _ := New(1, "JPY")
	if _, err := Sum(usd(1), jpyA); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("cross-currency Sum err = %v", err)
	}
}

func TestZeroAndIsZero(t *testing.T) {
	z, err := Zero("JPY")
	if err != nil || !z.IsZero() || z.Scale != 0 {
		t.Fatalf("Zero(JPY) = %+v, %v", z, err)
	}
	nz, _ := New(1, "JPY")
	if nz.IsZero() {
		t.Fatal("1 JPY reported as zero")
	}
}

func TestKnown(t *testing.T) {
	k := Known()
	sort.Slice(k, func(i, j int) bool { return k[i] < k[j] })
	want := []Currency{"EUR", "JPY", "USD"}
	if len(k) != len(want) {
		t.Fatalf("Known() = %v", k)
	}
	for i := range want {
		if k[i] != want[i] {
			t.Fatalf("Known() = %v, want %v", k, want)
		}
	}
}

func TestString(t *testing.T) {
	a, _ := New(-125000, "USD")
	if got := a.String(); got != "-125000 USD(scale 2)" {
		t.Fatalf("String() = %q", got)
	}
}
