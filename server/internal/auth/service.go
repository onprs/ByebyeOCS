// Package auth 提供用户与管理员认证、会话、登录挑战和外观/通知偏好接口。
//
// 边界：
//   - 不保存明文密码、原始 Cookie、明文平台会话或明文 TOTP 密钥；
//   - 平台会话只以 AES-GCM 密文落库，仅通过 PlatformSession 提供给服务内部适配器；
//   - 真实外部平台副作用由 Verifier 适配器决定，本包不发起真实平台请求；
//   - 生产环境不创建默认账号，管理员只能通过 CreateAdmin 显式建立。
package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 双端身份类型；与 web.Actor.Kind 一致。
const (
	KindUser  = "user"
	KindAdmin = "admin"
)

// 权限字符串：管理员首期使用固定 owner 权限，用户端只能操作自身资源。
const (
	PermissionOwner = "*"
	PermissionSelf  = "self"
)

// 会话通道：Web 使用 HttpOnly Cookie，App 使用 Bearer 访问令牌与刷新令牌。
const (
	ChannelWeb = "web"
	ChannelApp = "app"
)

// 工程侧默认值；均为可覆盖的设计参数，不是真实业务承诺。
const (
	defaultSessionTTL            = 12 * time.Hour
	defaultSessionIdle           = 7 * 24 * time.Hour
	defaultAppAccessTTL          = 15 * time.Minute
	defaultRefreshTTL            = 30 * 24 * time.Hour
	defaultChallengeTTL          = 5 * time.Minute
	defaultChallengeMaxAttempts  = 5
	defaultFreshWindow           = 10 * time.Minute
	defaultTOTPIssuer            = "ByebyeOCS"
	defaultTOTPPeriod            = 30 * time.Second
	defaultTOTPDigits            = 6
	defaultTOTPSkew              = 1
	defaultMaxFailures           = 5
	defaultRateWindow            = 15 * time.Minute
	defaultRateBlock             = 15 * time.Minute
	defaultChallengeMaxPerClient = 10
	defaultChallengeWindow       = 10 * time.Minute
	defaultChallengeMaxPending   = 5000
	defaultChallengeRetention    = time.Hour
	defaultPasswordConcurrency   = 2
	maxPasswordConcurrency       = 8
	defaultMaxSessionsPerSubject = 16
	maxSessionsPerSubjectLimit   = 64
	maxEncryptionKeys            = 8
	defaultReencryptBatchSize    = 200
	maxReencryptBatchSize        = 1000
	passwordBusyRetryAfter       = 2 * time.Second
	sessionTouchInterval         = time.Minute
)

// uuidPattern 只用于校验外部传入的编号，避免无效值触发数据库类型错误。
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// requestIDPattern 与 audit_events.request_id 约束保持一致。
var requestIDPattern = regexp.MustCompile(`^req_[0-9a-f]{32}$`)

// TOTPOptions 是 TOTP 策略参数，默认 30 秒、6 位、允许前后各一个时间窗口。
type TOTPOptions struct {
	Issuer string
	Period time.Duration
	Digits int
	Skew   int
}

// RateLimitOptions 是持久化登录限流参数。
type RateLimitOptions struct {
	MaxFailures int
	Window      time.Duration
	BlockFor    time.Duration
}

// ChallengeRateLimit 限制登录挑战创建：同客户端窗口总量、全局未完成总量与清理保留期。
type ChallengeRateLimit struct {
	MaxPerClient int
	Window       time.Duration
	MaxPending   int
	Retention    time.Duration
}

// Options 是认证服务配置。EncryptionKey 必须为 32 字节，seal 只使用当前版本；
// IdentityKey 用于派生外部身份索引与 CSRF，使主密钥轮换不改变主体归属。
type Options struct {
	CookieSecure bool
	UserOrigin   string
	AdminOrigin  string

	EncryptionKey        []byte
	EncryptionKeyVersion int

	// IdentityKey 是派生外部身份索引与 CSRF 的独立 32 字节密钥。
	// 为空时回退到 EncryptionKey（兼容既有单元测试）；生产应独立配置并长期保持稳定。
	// 轮换 IdentityKey 会改变主体索引与已签发会话的 CSRF 摘要，需先撤销全部会话。
	IdentityKey []byte

	// EncryptionKeys 是仅用于解密的旧版本密钥环（版本 → 32 字节密钥），最多 8 个。
	// 当前版本始终由 EncryptionKey/EncryptionKeyVersion 决定；密封只写当前版本。
	// 缺失对应版本时解密失败关闭，不会尝试其他密钥。
	EncryptionKeys map[int][]byte

	// ReencryptBatchSize 控制 ReencryptSecrets 每批处理行数（默认 200，1–1000）。
	ReencryptBatchSize int

	// Verifier 为外部平台适配器；nil 时用户外部登录返回 external_disabled。
	Verifier Verifier

	SessionTTL           time.Duration
	SessionIdle          time.Duration
	AppAccessTTL         time.Duration
	RefreshTTL           time.Duration
	ChallengeTTL         time.Duration
	ChallengeMaxAttempts int
	FreshWindow          time.Duration
	TOTP                 TOTPOptions
	RateLimit            RateLimitOptions
	ChallengeRateLimit   ChallengeRateLimit

	// ClientKey 提取登录挑战限流用的客户端标识；默认使用 RemoteAddr 主机部分，
	// 不信任 X-Forwarded-For。返回值只以摘要形式落库。
	ClientKey func(*http.Request) string

	// PasswordConcurrency 限制同时进行的管理员 Argon2id 密码校验数量（默认 2，上限 8）。
	// 饱和时登录与步进验证直接返回 429，不建立等待队列；CLI 本地哈希不受限。
	PasswordConcurrency int

	// MaxSessionsPerSubject 限制同一用户/管理员的最大在用会话数（默认 16，1–64）。
	// 登录签发新会话时按主体行锁串行执行；超限撤销最旧会话及其刷新链为新登录腾位。
	MaxSessionsPerSubject int

	// Enrollment 是 CLI 创建管理员时的一次性 TOTP 密钥交付通道。
	// 未提供且未显式传入密钥时，CreateAdmin 返回错误而不是静默丢弃密钥。
	Enrollment func(username, secret string) error

	// OnIdentityBound 在用户首次或再次可信登录时，于内部用户、外部身份和平台会话
	// 写入的同一事务内调用（userID 已确定、提交前）。返回错误会回滚整个登录事务，
	// 不遗留半绑定记录。nil 时保持原有行为。回调不得记录 Session 或 Subject。
	OnIdentityBound func(context.Context, pgx.Tx, string, VerifiedSubject) error

	// ExtraPublicPaths 允许协调者把能力发现、健康检查、支付回调等
	// 非认证公共路由加入匿名放行名单（精确路径匹配）。
	ExtraPublicPaths []string

	// Now 供测试固定时间；生产留空使用系统时间。
	Now func() time.Time
}

type origin struct {
	value string
	host  string
	https bool
}

func (o origin) matches(value string) bool {
	if value == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(value), o.value)
}

// Service 是认证与偏好模块入口。所有数据库写入都使用显式事务。
type Service struct {
	pool           *pgxpool.Pool
	options        Options
	userOrigin     origin
	adminOrigin    origin
	now            func() time.Time
	indexKey       []byte
	csrfKey        []byte
	sessions       sessionReader
	extraPublic    map[string]struct{}
	passwordSlots  chan struct{}
	identityKey    []byte
	encryptionKeys map[int][]byte
}

// New 构造认证服务。pool 必须由调用方传入，服务不修改连接配置。
func New(pool *pgxpool.Pool, options Options) (*Service, error) {
	if pool == nil {
		return nil, fault.New("auth_config_invalid", "认证服务需要数据库连接池")
	}
	return newService(pool, options)
}

func newService(pool *pgxpool.Pool, options Options) (*Service, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	userOrigin, err := parseOrigin(options.UserOrigin, "用户端")
	if err != nil {
		return nil, err
	}
	adminOrigin, err := parseOrigin(options.AdminOrigin, "管理员端")
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(userOrigin.value, adminOrigin.value) {
		return nil, fault.New("auth_config_invalid", "用户端与管理员端 Origin 必须不同")
	}
	if options.CookieSecure && !userOrigin.https {
		return nil, fault.New("auth_config_invalid", "启用安全 Cookie 时用户端 Origin 必须为 https")
	}
	if options.CookieSecure && !adminOrigin.https {
		return nil, fault.New("auth_config_invalid", "启用安全 Cookie 时管理员端 Origin 必须为 https")
	}
	identityKey := identityKeyFor(options)
	service := &Service{
		pool:           pool,
		options:        options,
		userOrigin:     userOrigin,
		adminOrigin:    adminOrigin,
		now:            options.Now,
		identityKey:    identityKey,
		encryptionKeys: copyKeyring(options.EncryptionKeys),
		indexKey:       deriveKey(identityKey, "bbo:index-key:v1"),
		csrfKey:        deriveKey(identityKey, "bbo:csrf-key:v1"),
		extraPublic:    map[string]struct{}{},
		passwordSlots:  make(chan struct{}, options.PasswordConcurrency),
	}
	for _, path := range options.ExtraPublicPaths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, " \t\r\n?#") {
			return nil, fault.New("auth_config_invalid", "额外公共路径必须是精确的以 / 开头的路径")
		}
		service.extraPublic[trimmed] = struct{}{}
	}
	service.sessions = sqlSessionReader{service: service}
	return service, nil
}

func normalizeOptions(options Options) (Options, error) {
	if len(options.EncryptionKey) != 32 {
		return Options{}, fault.New("auth_config_invalid", "认证加密密钥必须为 32 字节")
	}
	if options.EncryptionKeyVersion == 0 {
		options.EncryptionKeyVersion = 1
	}
	if options.EncryptionKeyVersion < 1 {
		return Options{}, fault.New("auth_config_invalid", "认证密钥版本必须为正整数")
	}
	options.EncryptionKey = append([]byte(nil), options.EncryptionKey...)

	if len(options.IdentityKey) != 0 {
		if len(options.IdentityKey) != 32 {
			return Options{}, fault.New("auth_config_invalid", "身份索引密钥必须为 32 字节")
		}
		options.IdentityKey = append([]byte(nil), options.IdentityKey...)
	}
	if len(options.EncryptionKeys) > maxEncryptionKeys {
		return Options{}, fault.New("auth_config_invalid", "解密密钥环最多允许 8 个版本")
	}
	keyring := make(map[int][]byte, len(options.EncryptionKeys))
	for version, key := range options.EncryptionKeys {
		if version < 1 {
			return Options{}, fault.New("auth_config_invalid", "解密密钥版本必须为正整数")
		}
		if len(key) != 32 {
			return Options{}, fault.New("auth_config_invalid", "解密密钥环中的每个密钥必须为 32 字节")
		}
		if version == options.EncryptionKeyVersion && !bytes.Equal(key, options.EncryptionKey) {
			return Options{}, fault.New("auth_config_invalid", "当前密钥版本的密钥环条目与 EncryptionKey 不一致")
		}
		keyring[version] = append([]byte(nil), key...)
	}
	options.EncryptionKeys = keyring

	if options.ReencryptBatchSize == 0 {
		options.ReencryptBatchSize = defaultReencryptBatchSize
	}
	if options.ReencryptBatchSize < 1 || options.ReencryptBatchSize > maxReencryptBatchSize {
		return Options{}, fault.New("auth_config_invalid", "密钥重封装批大小必须为 1 到 1000")
	}

	if options.SessionTTL == 0 {
		options.SessionTTL = defaultSessionTTL
	}
	if options.SessionIdle == 0 {
		options.SessionIdle = defaultSessionIdle
	}
	if options.AppAccessTTL == 0 {
		options.AppAccessTTL = defaultAppAccessTTL
	}
	if options.RefreshTTL == 0 {
		options.RefreshTTL = defaultRefreshTTL
	}
	if options.ChallengeTTL == 0 {
		options.ChallengeTTL = defaultChallengeTTL
	}
	if options.ChallengeMaxAttempts == 0 {
		options.ChallengeMaxAttempts = defaultChallengeMaxAttempts
	}
	if options.FreshWindow == 0 {
		options.FreshWindow = defaultFreshWindow
	}
	if options.SessionTTL < time.Minute || options.SessionTTL > 30*24*time.Hour {
		return Options{}, fault.New("auth_config_invalid", "会话有效期超出允许范围")
	}
	if options.SessionIdle < time.Minute || options.SessionIdle > 30*24*time.Hour {
		return Options{}, fault.New("auth_config_invalid", "会话空闲期限超出允许范围")
	}
	if options.AppAccessTTL < time.Minute || options.AppAccessTTL > 24*time.Hour {
		return Options{}, fault.New("auth_config_invalid", "App 访问令牌有效期超出允许范围")
	}
	if options.RefreshTTL < time.Hour || options.RefreshTTL > 180*24*time.Hour {
		return Options{}, fault.New("auth_config_invalid", "刷新令牌有效期超出允许范围")
	}
	if options.ChallengeTTL < 30*time.Second || options.ChallengeTTL > 15*time.Minute {
		return Options{}, fault.New("auth_config_invalid", "登录挑战有效期超出允许范围")
	}
	if options.ChallengeMaxAttempts < 1 || options.ChallengeMaxAttempts > 10 {
		return Options{}, fault.New("auth_config_invalid", "登录挑战尝试上限超出允许范围")
	}
	if options.FreshWindow < time.Minute || options.FreshWindow > 60*time.Minute {
		return Options{}, fault.New("auth_config_invalid", "新鲜验证窗口超出允许范围")
	}

	if options.TOTP.Issuer == "" {
		options.TOTP.Issuer = defaultTOTPIssuer
	}
	if options.TOTP.Period == 0 {
		options.TOTP.Period = defaultTOTPPeriod
	}
	if options.TOTP.Digits == 0 {
		options.TOTP.Digits = defaultTOTPDigits
	}
	if options.TOTP.Period < 15*time.Second || options.TOTP.Period > 2*time.Minute || options.TOTP.Period%time.Second != 0 {
		return Options{}, fault.New("auth_config_invalid", "TOTP 周期无效")
	}
	if options.TOTP.Digits != 6 && options.TOTP.Digits != 8 {
		return Options{}, fault.New("auth_config_invalid", "TOTP 位数只支持 6 或 8")
	}
	if options.TOTP.Skew < 0 || options.TOTP.Skew > 2 {
		return Options{}, fault.New("auth_config_invalid", "TOTP 时间窗口容差超出允许范围")
	}

	if options.RateLimit.MaxFailures == 0 {
		options.RateLimit.MaxFailures = defaultMaxFailures
	}
	if options.RateLimit.Window == 0 {
		options.RateLimit.Window = defaultRateWindow
	}
	if options.RateLimit.BlockFor == 0 {
		options.RateLimit.BlockFor = defaultRateBlock
	}
	if options.RateLimit.MaxFailures < 1 || options.RateLimit.MaxFailures > 100 {
		return Options{}, fault.New("auth_config_invalid", "登录失败次数上限超出允许范围")
	}
	if options.RateLimit.Window < time.Minute || options.RateLimit.Window > 24*time.Hour {
		return Options{}, fault.New("auth_config_invalid", "登录限流窗口超出允许范围")
	}
	if options.RateLimit.BlockFor < time.Minute || options.RateLimit.BlockFor > 24*time.Hour {
		return Options{}, fault.New("auth_config_invalid", "登录封禁时长超出允许范围")
	}

	if options.ChallengeRateLimit.MaxPerClient == 0 {
		options.ChallengeRateLimit.MaxPerClient = defaultChallengeMaxPerClient
	}
	if options.ChallengeRateLimit.Window == 0 {
		options.ChallengeRateLimit.Window = defaultChallengeWindow
	}
	if options.ChallengeRateLimit.MaxPending == 0 {
		options.ChallengeRateLimit.MaxPending = defaultChallengeMaxPending
	}
	if options.ChallengeRateLimit.Retention == 0 {
		options.ChallengeRateLimit.Retention = defaultChallengeRetention
	}
	if options.ChallengeRateLimit.MaxPerClient < 1 || options.ChallengeRateLimit.MaxPerClient > 100 {
		return Options{}, fault.New("auth_config_invalid", "单客户端挑战数量上限超出允许范围")
	}
	if options.ChallengeRateLimit.Window < time.Minute || options.ChallengeRateLimit.Window > time.Hour {
		return Options{}, fault.New("auth_config_invalid", "挑战限流窗口超出允许范围")
	}
	if options.ChallengeRateLimit.MaxPending < 100 || options.ChallengeRateLimit.MaxPending > 1_000_000 {
		return Options{}, fault.New("auth_config_invalid", "未完成挑战总量上限超出允许范围")
	}
	if options.ChallengeRateLimit.Retention < 10*time.Minute || options.ChallengeRateLimit.Retention > 24*time.Hour {
		return Options{}, fault.New("auth_config_invalid", "过期挑战保留期超出允许范围")
	}

	if options.PasswordConcurrency == 0 {
		options.PasswordConcurrency = defaultPasswordConcurrency
	}
	if options.PasswordConcurrency < 1 || options.PasswordConcurrency > maxPasswordConcurrency {
		return Options{}, fault.New("auth_config_invalid", "管理员密码校验并发必须为 1 到 8")
	}

	if options.MaxSessionsPerSubject == 0 {
		options.MaxSessionsPerSubject = defaultMaxSessionsPerSubject
	}
	if options.MaxSessionsPerSubject < 1 || options.MaxSessionsPerSubject > maxSessionsPerSubjectLimit {
		return Options{}, fault.New("auth_config_invalid", "单主体会话上限必须为 1 到 64")
	}

	if options.Now == nil {
		options.Now = time.Now
	}
	return options, nil
}

// identityKeyFor 返回用于索引/CSRF 派生的独立密钥；未配置时回退到当前加密密钥。
func identityKeyFor(options Options) []byte {
	if len(options.IdentityKey) != 0 {
		return options.IdentityKey
	}
	return options.EncryptionKey
}

func copyKeyring(keys map[int][]byte) map[int][]byte {
	result := make(map[int][]byte, len(keys))
	for version, key := range keys {
		result[version] = append([]byte(nil), key...)
	}
	return result
}

func parseOrigin(value, label string) (origin, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n") {
		return origin{}, fault.New("auth_config_invalid", label+" Origin 未配置")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return origin{}, fault.New("auth_config_invalid", label+" Origin 格式无效")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return origin{}, fault.New("auth_config_invalid", label+" Origin 只支持 http 或 https")
	}
	if parsed.Host == "" {
		return origin{}, fault.New("auth_config_invalid", label+" Origin 缺少主机")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return origin{}, fault.New("auth_config_invalid", label+" Origin 不能包含路径")
	}
	return origin{value: scheme + "://" + parsed.Host, host: parsed.Host, https: scheme == "https"}, nil
}

// originFor 返回对应端允许的浏览器来源。
func (s *Service) originFor(kind string) origin {
	if kind == KindAdmin {
		return s.adminOrigin
	}
	return s.userOrigin
}

// permissionFor 与 permissionsFor 定义在 roles.go。

// acquirePasswordSlot 非阻塞获取管理员密码校验名额。
// 饱和时调用方应直接返回 429，不得建立无界等待队列。
func (s *Service) acquirePasswordSlot() (release func(), ok bool) {
	select {
	case s.passwordSlots <- struct{}{}:
		return func() { <-s.passwordSlots }, true
	default:
		return nil, false
	}
}

// clientKey 返回限流用客户端标识；只保留主机部分，不信任转发头。
func (s *Service) clientKey(r *http.Request) string {
	value := ""
	if s.options.ClientKey != nil {
		value = strings.TrimSpace(s.options.ClientKey(r))
	} else {
		value = defaultClientKey(r)
	}
	if value == "" {
		return "unknown"
	}
	return value
}

func defaultClientKey(r *http.Request) string {
	address := strings.TrimSpace(r.RemoteAddr)
	if address == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}
	return strings.Trim(address, "[]")
}

func deriveKey(master []byte, label string) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte(label))
	return mac.Sum(nil)
}

func (s *Service) clock() time.Time { return s.now().UTC() }

// inTx 在显式事务中执行写入；失败时整批回滚。
func (s *Service) inTx(ctx context.Context, run func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fault.New("service_unavailable", "数据库事务无法开启，请稍后重试")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fault.New("service_unavailable", "数据库提交失败，请稍后重试")
	}
	return nil
}

// requestID 返回审计可用的请求编号；缺失时生成一次性编号，不改动请求上下文语义。
func requestID(ctx context.Context) string {
	if value := web.RequestID(ctx); requestIDPattern.MatchString(value) {
		return value
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return "req_" + hex.EncodeToString(raw[:])
}

func validUUID(value string) bool { return uuidPattern.MatchString(value) }

// PlatformSessionResolver 供平台适配器在服务内部解密用户的外部平台会话。
// 实现不得把返回值暴露给浏览器或普通 HTTP 响应。
type PlatformSessionResolver interface {
	PlatformSession(ctx context.Context, userID string) ([]byte, error)
}

var _ PlatformSessionResolver = (*Service)(nil)

// PlatformSession 返回最近一个有效平台会话的明文材料。
// 只供服务内部适配器使用；HTTP 层不调用该方法。
func (s *Service) PlatformSession(ctx context.Context, userID string) ([]byte, error) {
	if !validUUID(userID) {
		return nil, fault.New("validation_failed", "用户编号无效")
	}
	now := s.clock()
	var recordID, identityID string
	var version int
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, `SELECT id::text, external_identity_id::text, key_version, ciphertext
		FROM public.platform_sessions
		WHERE user_id = $1 AND status = 'active' AND expires_at > $2
		ORDER BY created_at DESC
		LIMIT 1`, userID, now).Scan(&recordID, &identityID, &version, &ciphertext)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New("platform_session_unavailable", "外部平台会话不可用，请重新验证身份")
		}
		return nil, fault.New("service_unavailable", "无法读取平台会话，请稍后重试")
	}
	plaintext, err := s.open(purposePlatformSession, []string{userID, identityID}, version, ciphertext)
	if err != nil {
		return nil, err
	}
	// 使用时间只作运维观察，不参与鉴权判断；更新失败不影响本次读取。
	_, _ = s.pool.Exec(ctx, `UPDATE public.platform_sessions SET last_used_at = $2, updated_at = $2
		WHERE id = $1 AND status = 'active'`, recordID, now)
	return plaintext, nil
}

// RequireFresh 校验会话来自近期完整验证，供导出、删除、资金等敏感操作复用。
func (s *Service) RequireFresh(w http.ResponseWriter, r *http.Request, kind string) (web.Actor, bool) {
	actor, ok := web.Require(w, r, kind, permissionFor(kind))
	if !ok {
		return web.Actor{}, false
	}
	if actor.AuthenticatedAt.IsZero() || s.clock().Sub(actor.AuthenticatedAt) > s.options.FreshWindow {
		web.Error(w, r, http.StatusForbidden, "reauth_required", "此操作需要重新验证身份")
		return web.Actor{}, false
	}
	return actor, true
}

// capabilities 只公布实际支持的能力；扫码尚未实现，明确返回 false。
func (s *Service) capabilities() map[string]any {
	methods := []string{}
	verification := "disabled"
	if s.options.Verifier != nil {
		methods = []string{"password"}
		verification = "configured"
	}
	return map[string]any{
		"identity": map[string]any{
			"provider":     ProviderChaoxing,
			"methods":      methods,
			"qr_scan":      false,
			"verification": verification,
		},
		"admin": map[string]any{
			"methods":  []string{"password+totp"},
			"rbac":     true,
			"roles":    roleNames,
			"step_up":  true,
			"owner_id": PermissionOwner,
		},
		"web_cookie":       true,
		"app_bearer":       true,
		"refresh_rotation": true,
		"totp":             true,
	}
}
