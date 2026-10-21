package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"golang.org/x/crypto/argon2"
)

// Argon2id 参数取工程侧默认值；密码哈希使用 PHC 字符串，便于后续升级参数。
const (
	argonMemoryKiB  = 64 * 1024
	argonTime       = 3
	argonThreads    = 2
	argonSaltLength = 16
	argonKeyLength  = 32

	passwordMinRunes = 12
	passwordMaxBytes = 1024
)

var dummyPasswordState struct {
	once sync.Once
	hash string
}

// hashPassword 生成 Argon2id PHC 字符串，包含参数、随机盐与哈希。
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fault.New("internal_error", "无法生成密码盐值")
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLength)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
	return encoded, nil
}

// verifyPassword 校验密码；格式损坏返回配置错误，密码不匹配返回 false, nil。
func verifyPassword(encoded, password string) (bool, error) {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(expected)))
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return false, nil
	}
	return true, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parsePasswordHash(encoded string) (argonParams, []byte, []byte, error) {
	var params argonParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params, nil, nil, errors.New("auth: 密码哈希格式无效")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params, nil, nil, errors.New("auth: 密码哈希版本无效")
	}
	var memory uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return params, nil, nil, errors.New("auth: 密码哈希参数无效")
	}
	if memory < 8*1024 || memory > 1024*1024 || timeCost < 1 || timeCost > 16 || threads < 1 || threads > 16 {
		return params, nil, nil, errors.New("auth: 密码哈希参数超出允许范围")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return params, nil, nil, errors.New("auth: 密码盐值无效")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 128 {
		return params, nil, nil, errors.New("auth: 密码哈希长度无效")
	}
	return argonParams{memory: memory, time: timeCost, threads: threads}, salt, key, nil
}

// verifyUnknownAccount 在账号不存在时执行同等成本的校验，减少耗时差异。
func verifyUnknownAccount(password string) {
	dummyPasswordState.once.Do(func() {
		hash, err := hashPassword("byebyeocs-placeholder-password")
		if err != nil {
			return
		}
		dummyPasswordState.hash = hash
	})
	if dummyPasswordState.hash == "" {
		return
	}
	_, _ = verifyPassword(dummyPasswordState.hash, password)
}

// validatePassword 校验管理员初始密码强度。
func validatePassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return fault.New("password_invalid", "管理员密码不能为空")
	}
	if len(password) > passwordMaxBytes {
		return fault.New("password_invalid", "管理员密码过长")
	}
	if utf8.RuneCountInString(password) < passwordMinRunes {
		return fault.New("password_weak", fmt.Sprintf("管理员密码至少需要 %d 个字符", passwordMinRunes))
	}
	distinct := map[rune]struct{}{}
	for _, r := range password {
		distinct[r] = struct{}{}
	}
	if len(distinct) < 4 {
		return fault.New("password_weak", "管理员密码需要更多不同字符")
	}
	return nil
}
