package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// TOTP 使用 RFC 6238 默认算法（HMAC-SHA1）与 RFC 4226 动态截断。
const totpSecretBytes = 20

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret 生成 160 位随机 TOTP 密钥，返回标准 Base32 大写无填充字符串。
// 返回值只允许通过一次性安全通道交付，不得写入日志或普通响应。
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fault.New("internal_error", "无法生成 TOTP 密钥")
	}
	return base32NoPadding.EncodeToString(raw), nil
}

// normalizeTOTPSecret 接受带空格或短横线的 Base32 密钥，输出规范形式。
func normalizeTOTPSecret(secret string) (string, error) {
	cleaned := strings.ToUpper(strings.TrimSpace(secret))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	if cleaned == "" {
		return "", fault.New("totp_secret_invalid", "TOTP 密钥不能为空")
	}
	decoded, err := base32NoPadding.DecodeString(cleaned)
	if err != nil {
		return "", fault.New("totp_secret_invalid", "TOTP 密钥必须是有效的 Base32 字符串")
	}
	if len(decoded) < 10 || len(decoded) > 64 {
		return "", fault.New("totp_secret_invalid", "TOTP 密钥长度超出允许范围")
	}
	return cleaned, nil
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	normalized, err := normalizeTOTPSecret(secret)
	if err != nil {
		return nil, err
	}
	return base32NoPadding.DecodeString(normalized)
}

// hotp 计算 RFC 4226 动态截断结果。
func hotp(secret []byte, counter uint64, digits int) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	modulus := uint32(1)
	for range digits {
		modulus *= 10
	}
	code := value % modulus
	return fmt.Sprintf("%0*d", digits, code)
}

// totpAt 计算指定时刻的 TOTP 值。
func totpAt(secret []byte, at time.Time, period time.Duration, digits int) string {
	if period <= 0 {
		period = defaultTOTPPeriod
	}
	counter := uint64(at.UTC().Unix()) / uint64(period/time.Second)
	return hotp(secret, counter, digits)
}

// matchTOTP 在允许的时间窗口内匹配验证码，返回命中的计数器用于防重放。
func matchTOTP(secret []byte, code string, at time.Time, options TOTPOptions) (uint64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != options.Digits {
		return 0, false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	period := uint64(options.Period / time.Second)
	if period == 0 {
		return 0, false
	}
	base := uint64(at.UTC().Unix()) / period
	matched := uint64(0)
	found := false
	for offset := -options.Skew; offset <= options.Skew; offset++ {
		candidate := int64(base) + int64(offset)
		if candidate < 0 {
			continue
		}
		expected := hotp(secret, uint64(candidate), options.Digits)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			matched = uint64(candidate)
			found = true
		}
	}
	return matched, found
}

// TOTPProvisioningURI 生成一次性交付给管理员的 otpauth URI。
// 该值包含 TOTP 密钥，调用方不得写日志或把 URI 存入数据库。
func TOTPProvisioningURI(issuer, account, secret string, options TOTPOptions) (string, error) {
	normalized, err := normalizeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(issuer) == "" {
		issuer = defaultTOTPIssuer
	}
	if strings.TrimSpace(account) == "" {
		return "", fault.New("totp_secret_invalid", "TOTP 账户名不能为空")
	}
	if options.Period == 0 {
		options.Period = defaultTOTPPeriod
	}
	if options.Digits == 0 {
		options.Digits = defaultTOTPDigits
	}
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	query := url.Values{}
	query.Set("secret", normalized)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", strconv.Itoa(options.Digits))
	query.Set("period", strconv.Itoa(int(options.Period/time.Second)))
	return "otpauth://totp/" + label + "?" + query.Encode(), nil
}
