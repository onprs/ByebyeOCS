package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// AES-GCM 关联数据的用途标签；不同用途不能互相替换。
const (
	purposePlatformSession = "platform_session"
	purposeSubject         = "external_subject"
	purposeAdminTOTP       = "admin_totp"
)

// 令牌前缀区分端与通道，避免用户令牌被管理员接口或刷新接口接受。
const (
	tokenPrefixUserWeb  = "bbu1_"
	tokenPrefixAdminWeb = "bba1_"
	tokenPrefixUserApp  = "bbat1_"
	tokenPrefixRefresh  = "bbrt1_"
)

const tokenBytes = 32

func issueToken(prefix string) (plain, digest string, err error) {
	var raw [tokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", fault.New("internal_error", "无法生成安全随机令牌")
	}
	plain = prefix + base64.RawURLEncoding.EncodeToString(raw[:])
	return plain, tokenDigest(plain), nil
}

func tokenDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// tokenPrefix 按端与通道返回允许的令牌前缀。
func tokenPrefix(kind, channel string) string {
	switch {
	case kind == KindAdmin:
		return tokenPrefixAdminWeb
	case channel == ChannelApp:
		return tokenPrefixUserApp
	default:
		return tokenPrefixUserWeb
	}
}

// tokenMatches 校验令牌前缀是否属于指定端与通道。
func tokenMatches(token, kind, channel string) bool {
	return strings.HasPrefix(token, tokenPrefix(kind, channel))
}

// csrfToken 由主密钥派生的子密钥与会话编号确定性生成，服务端只保存摘要。
// 会话轮换会改变会话编号，因此 CSRF 令牌随之失效。
func (s *Service) csrfToken(sessionID string) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	_, _ = mac.Write([]byte("bbo:csrf:v1|"))
	_, _ = mac.Write([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) csrfDigest(sessionID string) string {
	return tokenDigest(s.csrfToken(sessionID))
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

// aad 固定版本、用途与关联数据，防止密文在记录之间被搬移。
func aad(purpose string, version int, parts []string) []byte {
	var builder strings.Builder
	builder.WriteString("bbo:v1|")
	builder.WriteString(purpose)
	builder.WriteString("|")
	builder.WriteString(fmt.Sprintf("%d", version))
	for _, part := range parts {
		builder.WriteString("|")
		builder.WriteString(part)
	}
	return []byte(builder.String())
}

// seal 使用 AES-GCM 当前版本密钥加密敏感材料；返回密文与当前密钥版本。
func (s *Service) seal(purpose string, parts []string, plaintext []byte) ([]byte, int, error) {
	version := s.options.EncryptionKeyVersion
	aead, err := s.aeadFor(version)
	if err != nil {
		return nil, 0, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, 0, fault.New("internal_error", "无法生成加密随机数")
	}
	sealed := aead.Seal(nonce, nonce, plaintext, aad(purpose, version, parts))
	return sealed, version, nil
}

// open 按密文内嵌版本选择密钥解密；未知版本失败关闭，不尝试其他密钥。
func (s *Service) open(purpose string, parts []string, version int, ciphertext []byte) ([]byte, error) {
	aead, err := s.aeadFor(version)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aead.NonceSize()+aead.Overhead() {
		return nil, fault.New("secret_invalid", "加密材料格式无效")
	}
	nonce := ciphertext[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, ciphertext[aead.NonceSize():], aad(purpose, version, parts))
	if err != nil {
		return nil, fault.New("secret_invalid", "加密材料校验失败")
	}
	return plaintext, nil
}

// keyForVersion 返回指定版本的解密密钥；当前版本始终使用 EncryptionKey。
func (s *Service) keyForVersion(version int) ([]byte, error) {
	if version < 1 {
		return nil, fault.New("secret_key_version_unknown", "加密密钥版本无效")
	}
	if version == s.options.EncryptionKeyVersion {
		return s.options.EncryptionKey, nil
	}
	if key, ok := s.encryptionKeys[version]; ok && len(key) == 32 {
		return key, nil
	}
	return nil, fault.New("secret_key_version_unknown", "加密密钥版本未知，无法解密")
}

func (s *Service) aeadFor(version int) (cipher.AEAD, error) {
	key, err := s.keyForVersion(version)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fault.New("auth_config_invalid", "认证加密密钥无效")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fault.New("auth_config_invalid", "认证加密初始化失败")
	}
	return aead, nil
}
