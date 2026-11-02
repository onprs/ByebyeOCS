//go:build devfixture

package app

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
)

// localDevLoginVerifier 是仅用于本地 UI/API 联调的合成学习通验证器。
// 它不访问学习通，不产生平台会话，也不启用课程读取或任务执行；
// 只有带 devfixture 标签构建、development 模式、回环入口和显式环境变量同时满足时才启用。
func localDevLoginVerifier(cfg config.Config) (auth.Verifier, bool, error) {
	if cfg.Mode != "development" || os.Getenv("BYEBYEOCS_DEV_LOGIN") != "1" {
		return nil, false, nil
	}
	for _, origin := range []string{cfg.Auth.UserOrigin, cfg.Auth.AdminOrigin} {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "http" {
			return nil, false, nil
		}
		address := net.ParseIP(parsed.Hostname())
		if !strings.EqualFold(parsed.Hostname(), "localhost") && (address == nil || !address.IsLoopback()) {
			return nil, false, nil
		}
	}
	return &devLoginVerifier{namespace: "local-dev-v1"}, true, nil
}

type devLoginVerifier struct {
	namespace string
}

func (v *devLoginVerifier) Verify(_ context.Context, credentials auth.Credentials) (auth.VerifiedSubject, error) {
	account := strings.TrimSpace(credentials.Account)
	if account == "" || strings.TrimSpace(credentials.Password) == "" {
		return auth.VerifiedSubject{}, auth.ErrInvalidCredentials
	}
	school := strings.TrimSpace(credentials.School)
	if school == "" {
		school = "本地测试学校"
	}
	return auth.VerifiedSubject{
		Provider: auth.ProviderChaoxing, Namespace: v.namespace, Subject: "local:" + strings.ToLower(account),
		DisplayName: "本地测试用户 " + account, School: school,
		Session:   []byte("local-dev-session:" + account),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}
