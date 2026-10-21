package payments

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// 人民币元字符串严格解析为整数分，禁止 float64 乘 100。
// 最多 12 位整数、最多 2 位小数；不接受空白、符号或科学计数法。
var yuanPattern = regexp.MustCompile(`^([0-9]{1,12})(?:\.([0-9]{1,2}))?$`)

const maxYuanMinor = int64(100_000_000_000_000) // 1 万亿元，远低于 int64 上限

func ParseYuanToMinor(value string) (int64, error) {
	match := yuanPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, fault.New("validation_failed", "支付金额格式无效")
	}
	major, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || major > maxYuanMinor/100 {
		return 0, fault.New("validation_failed", "支付金额超出允许范围")
	}
	fraction := int64(0)
	if match[2] != "" {
		fractionText := match[2]
		if len(fractionText) == 1 {
			fractionText += "0"
		}
		fraction, err = strconv.ParseInt(fractionText, 10, 64)
		if err != nil {
			return 0, fault.New("validation_failed", "支付金额格式无效")
		}
	}
	minor := major*100 + fraction
	if minor <= 0 || minor > maxYuanMinor {
		return 0, fault.New("validation_failed", "支付金额必须大于零")
	}
	return minor, nil
}

// FormatYuan 固定两位小数，供 ZPay 请求和签名使用。
func FormatYuan(minor int64) string {
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	major := minor / 100
	fraction := minor % 100
	return sign + strconv.FormatInt(major, 10) + "." + pad2(fraction)
}

func pad2(value int64) string {
	if value < 10 {
		return "0" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}

func validMerchantOrderNo(value string) bool {
	if len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validProviderTradeNo(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') || r == '_' || r == '-' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}

func validCertificateText(value string, max int) bool {
	return len(value) >= 1 && len(value) <= max && strings.TrimSpace(value) == value
}
