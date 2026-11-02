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

func TestTieredCacheTokenPricingAndReservation(t *testing.T) {
	cacheRead, cacheWrite := Micros(100_000), Micros(1_500_000)
	price := &OfferingPricing{Version: "tiered-v1", InputMicrosPerMillionTokens: 1_000_000,
		OutputMicrosPerMillionTokens: 2_000_000, CacheReadMicrosPerMillionTokens: &cacheRead,
		CacheWriteMicrosPerMillionTokens: &cacheWrite, MicrosPerMinor: 10000,
		Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
	quote, err := priceUsageDetailed(price, 1_000_000, 10_000, 400_000, 100_000, "", false)
	if err != nil || quote.CostMicros != 710_000 || quote.AmountMinor != 71 || quote.CacheReadTokens != 400_000 || quote.CacheWriteTokens != 100_000 {
		t.Fatalf("缓存读写未按独立费率结算：%+v/%v", quote, err)
	}
	reservation, err := priceUsageDetailed(price, 1_000_000, 10_000, 0, 0, "ceil", true)
	if err != nil || reservation.CostMicros != 1_520_000 || reservation.AmountMinor != 152 {
		t.Fatalf("预占没有按输入侧最高缓存费率估算：%+v/%v", reservation, err)
	}
	if _, err := priceUsageDetailed(price, 100, 0, 80, 30, "", false); err == nil {
		t.Fatal("缓存 token 超过输入 token 时仍然报价")
	}
}

func TestCachePricingRequiresServerReportedBreakdown(t *testing.T) {
	cacheRead := Micros(100_000)
	price := &OfferingPricing{Version: "tiered-v1", InputMicrosPerMillionTokens: 1_000_000,
		OutputMicrosPerMillionTokens: 2_000_000, CacheReadMicrosPerMillionTokens: &cacheRead,
		MicrosPerMinor: 10000, Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
	input, output := int64(100), int64(10)
	_, err := (&Service{}).actualQuote(&Offering{Pricing: price}, Usage{InputTokens: &input, OutputTokens: &output})
	if err == nil {
		t.Fatal("缺少缓存读取 token 事实时仍然自动结算")
	}
}

// 零费率分项不需要上游返回用量：缺失时按 0 计，不影响报价。
func TestZeroRateCacheBreakdownIsOptional(t *testing.T) {
	zeroRead, zeroWrite := Micros(0), Micros(0)
	price := &OfferingPricing{Version: "zero-cache-v1", InputMicrosPerMillionTokens: 1_000_000,
		OutputMicrosPerMillionTokens: 2_000_000, CacheReadMicrosPerMillionTokens: &zeroRead,
		CacheWriteMicrosPerMillionTokens: &zeroWrite, MicrosPerMinor: 10000, Rounding: "ceil",
		Rule: "tokens_x_micros_per_million_v1"}
	input, output := int64(1_000_000), int64(10_000)
	quote, err := (&Service{}).actualQuote(&Offering{Pricing: price}, Usage{InputTokens: &input, OutputTokens: &output})
	if err != nil {
		t.Fatalf("零费率缓存分项缺失被当作计价失败: %v", err)
	}
	if quote.CacheReadTokens != 0 || quote.CacheWriteTokens != 0 || quote.CostMicros != 1_020_000 {
		t.Fatalf("零费率分项没有按 0 计价: %+v", quote)
	}
}
