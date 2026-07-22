package anomoly

import "testing"

func TestCurrencySymbolForUnit(t *testing.T) {
	cases := []struct {
		unit string
		want string
	}{
		{"USD", "$"},
		{"INR", "₹"},
		{"inr", "₹"},
		{" usd ", "$"},
		{"", "$"},
		{"EUR", "EUR "},
		{"gbp", "GBP "},
	}
	for _, c := range cases {
		if got := currencySymbolForUnit(c.unit); got != c.want {
			t.Errorf("currencySymbolForUnit(%q) = %q, want %q", c.unit, got, c.want)
		}
	}
}

func TestSpendAnomalyConfigMoney(t *testing.T) {
	cases := []struct {
		symbol string
		value  float64
		want   string
	}{
		{"$", 43923.46, "$43923.46"},
		{"₹", 43923.46, "₹43923.46"},
		{"", 10.5, "$10.50"}, // empty symbol defaults to $
		{"EUR ", 7.1, "EUR 7.10"},
	}
	for _, c := range cases {
		cfg := spendAnomalyConfig{CurrencySymbol: c.symbol}
		if got := cfg.money(c.value); got != c.want {
			t.Errorf("money(%v) with symbol %q = %q, want %q", c.value, c.symbol, got, c.want)
		}
	}
}
