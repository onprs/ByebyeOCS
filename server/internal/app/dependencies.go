package app

import (
	"github.com/onprs/ByebyeOCS/server/internal/adapters/browser"
	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing/authbridge"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/questions"
	"path/filepath"
	"time"
)

func defaultDependencies(cfg config.Config) (Dependencies, error) {
	result := Dependencies{}
	if !cfg.Auth.Enabled {
		return result, nil
	}
	if cfg.Payments.Enabled {
		provider, err := payments.NewZPay(payments.ZPayConfig{MerchantID: cfg.Payments.MerchantID, Key: cfg.Payments.Key, NotifyURL: cfg.Payments.NotifyURL, ReturnURL: cfg.Payments.ReturnURL})
		if err != nil {
			return result, err
		}
		result.PaymentProvider = provider
	}
	if cfg.Questions.Enabled {
		fork, err := questions.NewHTTPFork(cfg.Questions.URL, cfg.Questions.Token, cfg.Questions.Timeout)
		if err != nil {
			return result, err
		}
		result.Fork = fork
	}
	if cfg.Learning.Enabled {
		probe, err := chaoxing.NewJSONSubjectProbe(chaoxing.JSONProbeOptions{Enabled: true, Endpoint: cfg.Learning.SubjectProbeURL, Method: cfg.Learning.SubjectProbeMethod, Namespace: cfg.Learning.SubjectNamespace, EvidenceRef: cfg.Learning.AuthorizationReference, SubjectPath: cfg.Learning.SubjectPath, ExpiresAtPath: cfg.Learning.SessionExpiryPath, ExpiresAtFormat: cfg.Learning.SessionExpiryFormat, DisplayNamePath: cfg.Learning.DisplayNamePath, SchoolPath: cfg.Learning.SchoolPath})
		if err != nil {
			return result, fault.New("config_invalid", "可信平台会话探针配置无效")
		}
		verifier, err := chaoxing.NewVerifier(chaoxing.VerifierOptions{Enabled: true, LoginEnabled: true, ReadOnlyAuthorized: true, Namespace: cfg.Learning.SubjectNamespace, EvidenceRef: cfg.Learning.AuthorizationReference, SubjectProbe: probe})
		if err != nil {
			return result, fault.New("config_invalid", "平台登录验证器配置无效")
		}
		result.Verifier = authbridge.AuthVerifier{Verifier: verifier}
		platform, err := chaoxing.NewPlatform(chaoxing.Options{ReadOnlyEnabled: true})
		if err != nil {
			return result, fault.New("config_invalid", "原生课程读取适配配置无效")
		}
		result.Platform = platform
	}
	executor, err := buildExecutor(cfg)
	if err != nil {
		return result, err
	}
	result.Executor = executor
	return result, nil
}
func buildExecutor(cfg config.Config) (learning.Executor, error) {
	if !cfg.Learning.ExecutionEnabled {
		return nil, nil
	}
	executor, err := browser.New(browser.Options{Enabled: true, Authorized: cfg.Learning.ExecutionEvidenceReference != "", ChromePath: cfg.Learning.BrowserExecutable, ProfileRoot: filepath.Join(cfg.Storage.DataDir, "chrome-profiles"), ExecutionTimeout: 45 * time.Minute, MemoryLimitMB: 512, MaxProxyConnections: 16})
	if err != nil {
		return nil, fault.New("not_configured", "浏览器执行适配配置无效或环境不可用")
	}
	return executor, nil
}
