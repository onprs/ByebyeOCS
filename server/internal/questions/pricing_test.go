package questions

import "testing"

func TestRenminbiTokenPricingUnits(t *testing.T) {
	for _, test := range []struct {
		rounding    string
		rate        Micros
		tokens      int64
		cost, minor int64
	}{{"ceil", 1234567, 100, 124, 1}, {"floor", 1234567, 100, 123, 0}, {"ceil", 1234567, 1000000, 1234567, 124}, {"floor", 1234567, 1000000, 1234567, 123}, {"ceil", 0, 1000000, 0, 0}} {
		price := &OfferingPricing{Version: "currency-v1", InputMicrosPerMillionTokens: test.rate, MicrosPerMinor: 10000, Rounding: test.rounding, Rule: "tokens_x_micros_per_million_v1"}
		quote, err := priceUsage(price, test.tokens, 0, "")
		if err != nil || quote.CostMicros != test.cost || quote.AmountMinor != test.minor {
			t.Fatalf("人民币换算不一致：%#v，%v", quote, err)
		}
	}
	for _, divisor := range []Micros{0, 1, 100, 20000} {
		price := &OfferingPricing{Version: "invalid-v1", MicrosPerMinor: divisor, Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
		if price.Validate() == nil {
			t.Fatal("允许修改人民币分与微元的固定换算")
		}
	}
}
