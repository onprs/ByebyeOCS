package ledger

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

const (
	// 单笔金额上限远低于 bigint 上限，为累加和报价运算保留余量。
	maxAmountMinor = int64(1_000_000_000_000)
	microsPerMinor = int64(10_000)
)

func parseAmount(value string) (int64, error) {
	if value == "" || len(value) > 18 {
		return 0, fault.New("validation_failed", "金额必须是十进制整数字符串")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fault.New("validation_failed", "金额必须是十进制整数字符串")
		}
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount <= 0 || amount > maxAmountMinor {
		return 0, fault.New("validation_failed", "金额超出允许范围")
	}
	return amount, nil
}

func formatAmount(value int64) string { return strconv.FormatInt(value, 10) }

func formatSigned(value int64) string { return strconv.FormatInt(value, 10) }

func validateQuantity(value int64) error {
	if value <= 0 || value > maxAmountMinor {
		return fault.New("validation_failed", "用量数量必须为正整数")
	}
	return nil
}

func validateTokens(input, output int64) error {
	if input < 0 || output < 0 || input > maxAmountMinor || output > maxAmountMinor {
		return fault.New("validation_failed", "token 数量超出允许范围")
	}
	return nil
}

// serviceCostMicros 使用整数分子/分母计算，避免浮点累积和每次重试舍入差异。
func serviceCostMicros(quantityScaled, priceMicros int64, precision int) (*big.Int, error) {
	if quantityScaled <= 0 || priceMicros < 0 || precision < 0 || precision > 6 {
		return nil, fault.New("validation_failed", "服务计价参数无效")
	}
	numerator := new(big.Int).Mul(big.NewInt(quantityScaled), big.NewInt(priceMicros))
	denominator := pow10(precision)
	return new(big.Int).Quo(numerator, denominator), nil
}

func tikuCostMicros(inputTokens, outputTokens, inputPrice, outputPrice int64) (*big.Int, error) {
	if inputTokens < 0 || outputTokens < 0 || inputPrice < 0 || outputPrice < 0 {
		return nil, fault.New("validation_failed", "题库计价参数无效")
	}
	inputPart := new(big.Int).Mul(big.NewInt(inputTokens), big.NewInt(inputPrice))
	outputPart := new(big.Int).Mul(big.NewInt(outputTokens), big.NewInt(outputPrice))
	total := new(big.Int).Add(inputPart, outputPart)
	return total.Quo(total, big.NewInt(1_000_000)), nil
}

func pow10(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func microsToMinor(micros *big.Int, rounding string, minCharge int64) (int64, error) {
	if micros == nil || micros.Sign() < 0 {
		return 0, fault.New("validation_failed", "计费金额无效")
	}
	value := new(big.Int).Set(micros)
	switch rounding {
	case "floor":
	case "half_up":
		value.Add(value, big.NewInt(microsPerMinor/2))
	default:
		return 0, fault.New("validation_failed", "舍入规则无效")
	}
	value.Quo(value, big.NewInt(microsPerMinor))
	if !value.IsInt64() {
		return 0, fault.New("validation_failed", "计费金额超出允许范围")
	}
	minor := value.Int64()
	if minor < minCharge {
		minor = minCharge
	}
	if minor < 0 || minor > maxAmountMinor {
		return 0, fault.New("validation_failed", "计费金额超出允许范围")
	}
	return minor, nil
}

func ceilDiv(value *big.Int, divisor int64) (int64, error) {
	if value == nil || value.Sign() < 0 || divisor <= 0 {
		return 0, fault.New("validation_failed", "计费金额无效")
	}
	numerator := new(big.Int).Add(value, big.NewInt(divisor-1))
	numerator.Quo(numerator, big.NewInt(divisor))
	if !numerator.IsInt64() {
		return 0, fault.New("validation_failed", "计费金额超出允许范围")
	}
	return numerator.Int64(), nil
}

func bigInt64(value *big.Int, field string) (int64, error) {
	if value == nil || !value.IsInt64() {
		return 0, fault.New("validation_failed", field+"超出允许范围")
	}
	return value.Int64(), nil
}

func validRounding(value string) bool { return value == "floor" || value == "half_up" }

func trimSpace(value string) string { return strings.TrimSpace(value) }
