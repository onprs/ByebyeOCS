package ledger

import (
	"math"
	"math/big"
	"testing"
)

func TestTikuCostMatchesDocumentedExample(t *testing.T) {
	cost, err := tikuCostMicros(1284, 96, 3_000_000, 9_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if cost.String() != "4716" {
		t.Fatalf("成本 %s，期望 4716", cost)
	}
}

func TestServiceCostAndRounding(t *testing.T) {
	cost, err := serviceCostMicros(1, 3_000_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	minor, err := microsToMinor(cost, "floor", 0)
	if err != nil || minor != 300 {
		t.Fatalf("服务费用 %d,%v，期望 300 分", minor, err)
	}
	if got, _ := microsToMinor(big.NewInt(15_000), "floor", 0); got != 1 {
		t.Fatalf("floor 1.5 分 => %d", got)
	}
	if got, _ := microsToMinor(big.NewInt(15_000), "half_up", 0); got != 2 {
		t.Fatalf("half_up 1.5 分 => %d", got)
	}
	if got, _ := microsToMinor(big.NewInt(1), "floor", 2); got != 2 {
		t.Fatalf("最低收费 => %d", got)
	}
	if _, err := microsToMinor(big.NewInt(1), "unknown", 0); err == nil {
		t.Fatal("未知舍入规则被接受")
	}
}

func TestCostOverflowRejected(t *testing.T) {
	cost, err := tikuCostMicros(math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bigInt64(cost, "成本"); err == nil {
		t.Fatal("超范围成本未被拒绝")
	}
	if _, err := parseAmount("9999999999999999999"); err == nil {
		t.Fatal("超范围金额未被拒绝")
	}
}

func TestBuildQuoteUnconfiguredAndDisabled(t *testing.T) {
	quote, err := buildQuote(ScopeServiceTaskPoint, nil, billingSettings{ServiceChargeEnabled: true}, true, 1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if quote.State != "unpriced" || quote.Chargeable || quote.ChargeMinor != "0" {
		t.Fatalf("未配置价格报价错误: %+v", quote)
	}
	price := &priceSnapshot{ID: "p", Version: 1, Scope: ScopeTikuTokens, UnitLabel: "token",
		InputMicrosPerMillion: 3_000_000, OutputMicrosPerMillion: 9_000_000,
		SettlementRule: "accumulate_threshold", Rounding: "floor", SettlementThresholdMicros: 10000}
	quote, err = buildQuote(ScopeTikuTokens, price, billingSettings{TikuChargeEnabled: false}, true, 0, 1284, 96)
	if err != nil {
		t.Fatal(err)
	}
	if quote.State != "disabled" || quote.Chargeable || quote.CostMicros != "4716" {
		t.Fatalf("关闭计费报价错误: %+v", quote)
	}
	quote, err = buildQuote(ScopeTikuTokens, price, billingSettings{TikuChargeEnabled: true}, true, 0, 1284, 96)
	if err != nil {
		t.Fatal(err)
	}
	if quote.State != "quoted" || !quote.Chargeable || quote.ChargeMinor != "0" || quote.MaxChargeMinor != "1" {
		t.Fatalf("题库报价错误: %+v", quote)
	}
}

func TestParseAmountRejectsNonCanonical(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "+1", "1.0", " 1", "1 ", "01x"} {
		if _, err := parseAmount(value); err == nil {
			t.Fatalf("金额 %q 被接受", value)
		}
	}
	if got, err := parseAmount("01"); err != nil || got != 1 {
		t.Fatalf("前导零处理错误: %d,%v", got, err)
	}
}

func TestBuildQuoteRejectsNonMVPThreshold(t *testing.T) {
	price := &priceSnapshot{ID: "p", Version: 1, Scope: ScopeTikuTokens, UnitLabel: "token",
		InputMicrosPerMillion: 1_000_000, OutputMicrosPerMillion: 1_000_000,
		SettlementRule: "accumulate_threshold", Rounding: "floor", SettlementThresholdMicros: 20000}
	if _, err := buildQuote(ScopeTikuTokens, price,
		billingSettings{TikuChargeEnabled: true}, true, 0, 20000, 0); err == nil {
		t.Fatal("threshold=20000 未被拒绝")
	}
}
