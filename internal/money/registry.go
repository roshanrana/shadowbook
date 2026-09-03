package money

// The currency registry. Scale lives here and nowhere else, so a currency's
// minor-unit exponent is stated exactly once in the Go tree.
//
// JPY has scale 0. Q10 (jpy_two_decimals_truncated) seeds the legacy core
// storing it at scale 2; there is no exported way to reproduce that here.
var registry = map[Currency]uint8{
	"USD": 2,
	"EUR": 2,
	"JPY": 0,
}

func scaleOf(c Currency) (uint8, bool) {
	s, ok := registry[c]
	return s, ok
}

// Known returns the registered currencies. Used by tests and by the simulator's
// configuration validation; the returned order is not guaranteed, so callers
// that emit it must sort.
func Known() []Currency {
	out := make([]Currency, 0, len(registry))
	for c := range registry {
		out = append(out, c)
	}
	return out
}
