package learning

import (
	"log/slog"
	"time"
)

// Options 是学习服务运行参数。外部能力和执行均默认关闭。
type Options struct {
	Platform Platform
	Executor Executor
	Verifier Verifier
	Sessions SessionResolver
	Billing  Billing
	// BillingConfirmationRecorder 可选；确认时记录未定价/0 价/已收费任务的中央用量事实。
	BillingConfirmationRecorder BillingConfirmationRecorder
	// BillingSettlementReporter 可选；app 接线后按账本实际 settled/released 更新任务。
	BillingSettlementReporter BillingSettlementReporter
	// IdentityFingerprint 为必填的身份键派生函数；未配置时不接受身份绑定。
	IdentityFingerprint IdentityFingerprint
	// QuestionsPolicyValidator 可选；启用/选择模型时必须由题库模块确认公开条目。
	QuestionsPolicyValidator QuestionsPolicyValidator
	Logger                   *slog.Logger
	InstanceID               string
	Now                      func() time.Time

	// ExecutionEnabled 必须显式打开；仅提供 Executor 不视为授权真实执行。
	ExecutionEnabled bool
	// GlobalConcurrency 与 AccountConcurrency 是平台请求/尝试上限，不是平台承诺。
	GlobalConcurrency    int
	AccountConcurrency   int
	LeaseTTL             time.Duration
	HeartbeatInterval    time.Duration
	PollInterval         time.Duration
	VerificationInterval time.Duration
	// PreflightTimeout 限制外部执行前的只读平台核验。
	PreflightTimeout time.Duration
	RetryBase        time.Duration
	MaxAttempts      int
	MaxVerifications int
	// RecoveryRetry 为 true 时，崩溃后先核验、未通过且执行器声明安全恢复才重新调度。
	RecoveryRetry bool
	// CourseSnapshotTTL 仅用于展示快照新鲜度，不阻止读取旧快照。
	CourseSnapshotTTL time.Duration
	// SyncBatch 是单次课程同步最多处理的课程数，0 表示不额外限制。
	SyncBatch int
}

func (o *Options) withDefaults() {
	if o.GlobalConcurrency <= 0 {
		o.GlobalConcurrency = 8
	}
	if o.AccountConcurrency <= 0 {
		o.AccountConcurrency = 2
	}
	if o.LeaseTTL <= 0 {
		o.LeaseTTL = 45 * time.Second
	}
	if o.HeartbeatInterval <= 0 {
		o.HeartbeatInterval = 10 * time.Second
	}
	if o.PollInterval <= 0 {
		o.PollInterval = time.Second
	}
	if o.VerificationInterval <= 0 {
		o.VerificationInterval = 5 * time.Second
	}
	if o.RetryBase <= 0 {
		o.RetryBase = 30 * time.Second
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	if o.MaxVerifications <= 0 {
		o.MaxVerifications = 3
	}
	if o.PreflightTimeout <= 0 {
		o.PreflightTimeout = 20 * time.Second
	}
	if o.CourseSnapshotTTL <= 0 {
		o.CourseSnapshotTTL = 12 * time.Hour
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	if o.InstanceID == "" {
		o.InstanceID = newID()
	}
}

func (o *Options) validate() error {
	if o.ExecutionEnabled && o.Executor == nil {
		return validation("已启用执行但未提供执行器")
	}
	if o.ExecutionEnabled && o.Sessions == nil {
		return validation("已启用执行但未提供平台会话解析器")
	}
	if o.HeartbeatInterval >= o.LeaseTTL {
		return validation("心跳间隔必须小于租约时长")
	}
	if o.PreflightTimeout > 2*time.Minute {
		return validation("执行前核验超时过长")
	}
	if o.GlobalConcurrency > 256 || o.AccountConcurrency > 64 {
		return validation("并发上限超出允许范围")
	}
	return nil
}

func (s *Service) questionExecutionAvailable() bool {
	return s.opts.ExecutionEnabled && s.opts.Executor != nil &&
		s.opts.Executor.Capabilities().Enabled && s.opts.Executor.Capabilities().Supports("quiz")
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
