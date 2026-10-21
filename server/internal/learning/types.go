package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 任务状态。平台确认与执行观察是独立事实，状态只表达调度语义。
const (
	StatePending             = "pending"
	StatePreparing           = "preparing"
	StateRunning             = "running"
	StateWaitingVerification = "waiting_verification"
	StateConfirmed           = "confirmed"
	StatePaused              = "paused"
	StateNeedsAttention      = "needs_attention"
	StateFailed              = "failed"
	StateCancelled           = "cancelled"
)

const (
	DesiredRun    = "run"
	DesiredPause  = "pause"
	DesiredCancel = "cancel"
)

const (
	CheckConfirmed   = "confirmed"
	CheckUnconfirmed = "unconfirmed"
	CheckUnknown     = "unknown"
)

// FailureClass 是执行器可分类上报的失败类型。未知错误按有界临时失败处理。
type FailureClass string

const (
	FailureTransient          FailureClass = "transient"
	FailureRateLimited        FailureClass = "rate_limited"
	FailureVerificationNeeded FailureClass = "verification_required"
	FailureIdentityInvalid    FailureClass = "identity_invalid"
	FailureForbidden          FailureClass = "forbidden"
	FailureCapabilityMissing  FailureClass = "capability_missing"
	FailureInvalidTask        FailureClass = "invalid_task"
	FailurePermanent          FailureClass = "permanent"
	FailureStopped            FailureClass = "stopped"
	FailureCancelled          FailureClass = "cancelled"
)

// Failure 只携带可安全展示的分类和摘要，不包含上游原始响应、Cookie 或地址。
type Failure struct {
	Class      FailureClass
	RetryAfter time.Duration
	Detail     string
}

func (f *Failure) Error() string {
	if f.Detail == "" {
		return string(f.Class)
	}
	return f.Detail
}

// Fail 构造执行器/适配器可分类的失败。
func Fail(class FailureClass, detail string) *Failure {
	return &Failure{Class: class, Detail: detail}
}

func failureOf(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure
	}
	return &Failure{Class: FailureTransient, Detail: "执行未完成"}
}

// Actor 直接复用认证服务的上下文主体；用户过滤只来自服务端会话。
type Actor = web.Actor

// SessionResolver 通过认证服务解密平台会话；业务层不接触主密钥。
type SessionResolver interface {
	PlatformSession(ctx context.Context, userID string) ([]byte, error)
}

// IdentityFingerprint 把可信会话确认的稳定主体转换为不可逆 HMAC 指纹。
// 实现必须使用认证主密钥派生的独立子密钥，不能使用随机值、用户名或裸哈希。
// 返回的指纹会持久化并参与任务稳定组合键；原始主体只允许停留在请求内存和
// 认证服务加密的平台会话中。
type IdentityFingerprint func(namespace, subject string) string

// SessionResolverFunc 便于接线和测试。
type SessionResolverFunc func(ctx context.Context, userID string) ([]byte, error)

func (f SessionResolverFunc) PlatformSession(ctx context.Context, userID string) ([]byte, error) {
	return f(ctx, userID)
}

// PlatformCourse 是一级课程引用。
type PlatformCourse struct {
	ExternalCourseID string
	ExternalClassID  string
	CPI              string
	Title            string
	Teacher          string
	Term             string
}

// PlatformCourseRef 用于读取指定课程与任务卡片。
type PlatformCourseRef struct {
	ExternalCourseID string
	ExternalClassID  string
	CPI              string
	CycleKey         string
}

// PlatformAttachment 是平台附件。TaskPointID 为空表示普通学习资料，不计未完成。
type PlatformAttachment struct {
	ObjectID        string
	TaskPointID     string
	Kind            string
	Title           string
	DurationSeconds float64
	PlayedSeconds   float64
	Passed          bool
	PassedKnown     bool
	SortOrder       int
}

type PlatformChapter struct {
	ExternalID       string
	ParentExternalID string
	Title            string
	SortOrder        int
	Attachments      []PlatformAttachment
}

type PlatformCourseSnapshot struct {
	Course   PlatformCourseRef
	Title    string
	Teacher  string
	Term     string
	Adapter  string
	Chapters []PlatformChapter
	// SkippedAttachments 记录缺少稳定标识而未建目录条目的附件数量。
	SkippedAttachments int
}

// PlatformTaskRef 是只读核验所需的稳定任务引用。
type PlatformTaskRef struct {
	Course            PlatformCourseRef
	ChapterExternalID string
	ObjectID          string
	TaskPointID       string
}

// PlatformCheck 是平台返回的新鲜查询事实。只有 CheckConfirmed 才能推进为已确认。
type PlatformCheck struct {
	Status          string
	Passed          bool
	CheckedAt       time.Time
	Source          string
	PlayTimeSeconds *float64
	Note            string
}

type PlatformCapabilities struct {
	Adapter             string
	CourseRead          bool
	TaskCheck           bool
	CredentialVerify    bool
	ReadRequiresSession bool
	DisabledReason      string
}

// Platform 是只读平台适配契约。执行副作用必须通过 Executor，不得藏在读取接口。
type Platform interface {
	Capabilities() PlatformCapabilities
	ListCourses(ctx context.Context, session []byte) ([]PlatformCourse, error)
	DiscoverCourse(ctx context.Context, session []byte, course PlatformCourseRef) (PlatformCourseSnapshot, error)
	CheckTask(ctx context.Context, session []byte, ref PlatformTaskRef) (PlatformCheck, error)
}

// Observation 是执行器观察，不构成平台确认。
type Observation struct {
	Fraction        *float64  `json:"fraction,omitempty"`
	PositionSeconds *float64  `json:"position_seconds,omitempty"`
	DurationSeconds *float64  `json:"duration_seconds,omitempty"`
	Source          string    `json:"source"`
	ObservedAt      time.Time `json:"observed_at"`
	Note            string    `json:"note,omitempty"`
}

// MediaEvidence 只能由真实播放执行器产生；默认不可用。
type MediaEvidence struct {
	Available  bool      `json:"available"`
	Source     string    `json:"source,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

func unavailableMedia() MediaEvidence { return MediaEvidence{Available: false} }

type ExecutorCapabilities struct {
	Name                  string
	Enabled               bool
	TaskKinds             []string
	ProducesMediaEvidence bool
	SafeLeaseRecovery     bool
	DisabledReason        string
}

func (c ExecutorCapabilities) Supports(kind string) bool {
	for _, item := range c.TaskKinds {
		if item == kind {
			return true
		}
	}
	return false
}

// QuestionPolicy 是安排版本的题库策略快照。当前没有答题执行/提交能力，
// 该快照只冻结用户选择，不触发模型调用，也不代表自动提交可用。
type QuestionPolicy struct {
	Enabled                   bool
	ModelOfferingID           string
	Mode                      string
	RequireManualConfirmation bool
}

// QuestionsPolicyValidator 由题库模块接线，确认公开模型条目已发布且模式可用。
// 客户端不能提交 upstream/provider/key/价格等内部字段。
type QuestionsPolicyValidator interface {
	ValidateQuestionPolicy(ctx context.Context, modelOfferingID, mode string) error
}

type QuestionsPolicyValidatorFunc func(ctx context.Context, modelOfferingID, mode string) error

func (f QuestionsPolicyValidatorFunc) ValidateQuestionPolicy(ctx context.Context, modelOfferingID, mode string) error {
	return f(ctx, modelOfferingID, mode)
}

// ExecutionRequest 携带已领取尝试、绑定安排版本和平台会话。
type ExecutionRequest struct {
	AttemptID      string
	TaskID         string
	UserID         string
	FenceToken     int64
	Session        []byte
	Task           PlatformTaskRef
	PlanRevision   string
	Priority       int
	QuestionPolicy QuestionPolicy
	// MediaDurationSeconds 是课程快照中的附件时长；0 表示未知。
	MediaDurationSeconds float64
	// StopRequested 在用户/管理员请求暂停或取消后为 true，执行器必须安全确认后返回。
	StopRequested bool
}

// ExecutionResult 由执行器返回。Completed 仍不等于平台确认。
type ExecutionResult struct {
	Completed        bool
	StopAcknowledged bool
	Observation      Observation
	Media            MediaEvidence
}

type Executor interface {
	Capabilities() ExecutorCapabilities
	Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error)
}

type VerificationRequest struct {
	TaskID       string
	UserID       string
	AttemptID    string
	Session      []byte
	Task         PlatformTaskRef
	Verification int
}

// Verifier 只做读取核对；不得通过提交、播放或隐藏接口改变平台状态。
type Verifier interface {
	Verify(ctx context.Context, req VerificationRequest) (PlatformCheck, error)
}

// BillingQuote 由账本服务报价。Configured=false 表示不可预占，不得按 0 元扣费；
// State 用于区分 disabled/unpriced/quoted，空值兼容旧实现。
type BillingQuote struct {
	Configured   bool   `json:"configured"`
	State        string `json:"state,omitempty"`
	AmountMinor  int64  `json:"amount_minor"`
	Currency     string `json:"currency"`
	PriceVersion string `json:"price_version"`
}

// BillingConfirmationRecorder 可选记录平台确认后的中央用量事实。
// 必须按任务原冻结报价记录，幂等写 usage 事实；不得用确认时的新价格倒灌。
// 实现与任务确认在同一父事务的保存点内执行，失败不能抹掉平台确认。
type BillingConfirmationRecorder interface {
	RecordConfirmed(ctx context.Context, tx pgx.Tx, userID, taskID string) error
}

// BillingSettlementReporter 可选描述结算实际结果。app 用它持久 usage 事实后结算，
// 返回 settled/released/needs_review；存在时不再调用 Billing.Settle。
type BillingSettlementReporter interface {
	SettleState(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key string, amountMinor int64) (string, error)
}

// Billing 与 ledger 模块接线。所有调用必须使用调用方事务，保证任务与预占一致。
type Billing interface {
	Quote(ctx context.Context, tx pgx.Tx, userID, taskID, taskKind string) (BillingQuote, error)
	Reserve(ctx context.Context, tx pgx.Tx, userID, taskID, key string, quote BillingQuote) (string, error)
	Settle(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key string, amountMinor int64) error
	Release(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key, reason string) error
}

// PlanScope 是安排范围。selected 时 chapters 必须属于 course_ids。
type PlanScope struct {
	Kind     string        `json:"kind"`
	Chapters []PlanChapter `json:"chapters,omitempty"`
}

type PlanChapter struct {
	CourseID  string `json:"course_id"`
	ChapterID string `json:"chapter_id"`
}

// AuditEvent 是管理员操作的同事务审计事实。默认写入 public.audit_events。
type AuditEvent struct {
	Actor        Actor
	Action       string
	ResourceType string
	ResourceID   string
	Reason       string
	RequestID    string
}

// IDs 使用与 web 一致的随机 UUID v4，不依赖数据库自增，也不假设提交顺序。
func newID() string { return web.NewID() }

func requestHash(parts ...string) string {
	// 仅用于命令幂等摘要，不是密码学签名。
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
