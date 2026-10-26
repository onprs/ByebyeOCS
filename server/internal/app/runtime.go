package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/events"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/operations"
	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/platform/postgres"
	"github.com/onprs/ByebyeOCS/server/internal/questions"
	"github.com/onprs/ByebyeOCS/server/internal/transport/httpapi"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// Dependencies 是受信任的进程内扩展点，HTTP 和配置不能注入任意实现。
type Dependencies struct {
	Verifier        auth.Verifier
	Platform        learning.Platform
	Executor        learning.Executor
	Fork            questions.ForkClient
	PaymentProvider payments.Provider
}
type Runtime struct {
	userLoginAvailable bool
	cfg                config.Config
	db                 *postgres.Database
	logger             *slog.Logger
	Auth               *auth.Service
	Learning           *learning.Service
	Ledger             *ledger.Service
	Questions          *questions.Service
	Operations         *operations.Service
	Events             *events.Service
	HTTP               HTTPOptions
}

func NewRuntime(cfg config.Config, db *postgres.Database, logger *slog.Logger) (*Runtime, error) {
	deps, err := defaultDependencies(cfg)
	if err != nil {
		return nil, err
	}
	return Assemble(cfg, db, logger, deps)
}
func Assemble(cfg config.Config, db *postgres.Database, logger *slog.Logger, deps Dependencies) (*Runtime, error) {
	runtime := &Runtime{cfg: cfg, db: db, logger: logger, userLoginAvailable: deps.Verifier != nil}
	runtime.HTTP.User.AssetsDir = cfg.Storage.ClientAssetsDir
	runtime.HTTP.Admin.AssetsDir = cfg.Storage.AdminAssetsDir
	runtime.HTTP.User.LongRequestTimeout = cfg.Questions.Timeout + 15*time.Second
	runtime.HTTP.Admin.LongRequestTimeout = cfg.Questions.Timeout + 15*time.Second
	if !cfg.Auth.Enabled {
		return runtime, nil
	}
	identityKey := cfg.Auth.IdentityKey
	if len(identityKey) == 0 {
		identityKey = cfg.Auth.EncryptionKey
	}
	exportKeys := [][]byte{}
	for version, key := range cfg.Auth.EncryptionKeys {
		if version != cfg.Auth.EncryptionKeyVersion {
			exportKeys = append(exportKeys, deriveKey(key, "privacy-export"))
		}
	}
	ops, err := operations.New(db.Pool(), operations.Options{Logger: logger, RequestTimeout: cfg.Database.QueryTimeout, QuestionCleanup: func(ctx context.Context) error {
		if runtime.Questions == nil {
			return nil
		}
		_, err := runtime.Questions.RunCleanup(ctx)
		return err
	}, DataDir: cfg.Storage.DataDir, Key: deriveKey(cfg.Auth.EncryptionKey, "privacy-export"), DecryptionKeys: exportKeys, ExportMaxBytes: cfg.Operations.ExportMaxBytes, RetentionInterval: cfg.Operations.RetentionInterval, DiskProtection: cfg.Operations.DiskProtection, RequireFreshBackup: cfg.Operations.RequireFreshBackup, BackupStatusFile: cfg.Operations.BackupStatusFile, BackupMaxAge: cfg.Operations.BackupMaxAge, QuestionConcurrency: cfg.Questions.Concurrency, Runtime: map[string]any{"mode": cfg.Mode, "global_concurrency": cfg.Learning.GlobalConcurrency, "account_concurrency": cfg.Learning.AccountConcurrency, "source_configured": deps.Platform != nil, "executor_configured": deps.Executor != nil, "payments_configured": cfg.Payments.Enabled, "question_service_configured": cfg.Questions.Enabled}})
	if err != nil {
		return nil, err
	}
	if err := ops.Prepare(); err != nil {
		return nil, err
	}
	runtime.Operations = ops
	chargingAllowed := cfg.Payments.ChargingEnabled
	book, err := ledger.New(db.Pool(), ledger.Options{Provider: deps.PaymentProvider, ChargingAllowed: &chargingAllowed, Gate: ops.Gate})
	if err != nil {
		return nil, err
	}
	runtime.Ledger = book
	trusted := []netip.Prefix{}
	for _, text := range cfg.Server.TrustedProxies {
		value, err := netip.ParsePrefix(text)
		if err != nil {
			return nil, fault.New("config_invalid", "受信任代理配置无效")
		}
		trusted = append(trusted, value)
	}
	subjectKey := deriveKey(identityKey, "learning-subject-index")
	identity, err := auth.New(db.Pool(), auth.Options{CookieSecure: cfg.Auth.CookieSecure, UserOrigin: cfg.Auth.UserOrigin, AdminOrigin: cfg.Auth.AdminOrigin, EncryptionKey: cfg.Auth.EncryptionKey, IdentityKey: cfg.Auth.IdentityKey, EncryptionKeys: cfg.Auth.EncryptionKeys, EncryptionKeyVersion: cfg.Auth.EncryptionKeyVersion, Verifier: deps.Verifier, SessionTTL: cfg.Auth.SessionTTL, ClientKey: func(r *http.Request) string { return web.ClientKey(r, trusted) }, OnIdentityBound: func(ctx context.Context, tx pgx.Tx, userID string, subject auth.VerifiedSubject) error {
		if runtime.Learning == nil {
			return fault.New("service_unavailable", "课程身份服务尚未准备好")
		}
		return runtime.Learning.BindAccountTx(ctx, tx, userID, learning.AccountIdentity{Provider: subject.Provider, Namespace: subject.Namespace, Subject: subject.Subject, DisplayName: subject.DisplayName, School: subject.School, VerifiedAt: time.Now().UTC()})
	}})
	if err != nil {
		return nil, err
	}
	runtime.Auth = identity
	ops.SetRevokeUser(identity.RevokeUser)
	executor := deps.Executor
	automaticAnswers := &workAnswerer{}
	if cfg.Learning.ExecutionEnabled && cfg.Questions.Enabled {
		if workPlatform, ok := deps.Platform.(learning.WorkPlatform); ok {
			executor, err = learning.NewAutomaticExecutor(db.Pool(), learning.AutomaticExecutorOptions{
				Enabled: true, Platform: workPlatform, Answerer: automaticAnswers,
				Fallback: deps.Executor, BeforeExternal: ops.CheckExternalRequest, AcquireQuestion: ops.WaitForQuestion,
			})
			if err != nil {
				return nil, err
			}
		}
	}
	if cached, ok := deps.Platform.(learning.ResourceCachePlatform); ok {
		cached.SetResourceCache(learning.NewCourseResourceCache(db.Pool()))
	}
	learn, err := learning.New(db.Pool(), &learning.Options{Sessions: identity, Platform: deps.Platform, Executor: executor, ExecutionEnabled: cfg.Learning.ExecutionEnabled, ShutdownTimeout: cfg.Server.ShutdownTimeout, RequestTimeout: cfg.Database.QueryTimeout, RecoveryRetry: true, Logger: logger, Billing: &taskBilling{book: book}, BillingConfirmationRecorder: &taskBilling{book: book}, BillingSettlementReporter: &taskBilling{book: book}, QuestionsPolicyValidator: learning.QuestionsPolicyValidatorFunc(func(ctx context.Context, modelID, mode string) error {
		if runtime.Questions == nil {
			return fault.New("service_unavailable", "题库模型服务尚未准备好")
		}
		return runtime.Questions.ValidateQuestionPolicy(ctx, modelID, mode)
	}), GlobalConcurrency: cfg.Learning.GlobalConcurrency, AccountConcurrency: cfg.Learning.AccountConcurrency, IdentityFingerprint: func(namespace, subject string) string {
		sum := hmac.New(sha256.New, subjectKey)
		sum.Write([]byte(namespace))
		sum.Write([]byte{0})
		sum.Write([]byte(subject))
		return hex.EncodeToString(sum.Sum(nil))
	}})
	if err != nil {
		return nil, err
	}
	runtime.Learning = learn
	answers, err := questions.New(db.Pool(), questions.Options{Fork: deps.Fork, Billing: &questionBilling{book: book}, AllowSharedCache: cfg.Questions.AllowSharedCache, AutomaticCharging: cfg.Payments.ChargingEnabled, AutomaticChargingAllowed: func(ctx context.Context) (bool, error) {
		if !cfg.Payments.ChargingEnabled || !cfg.Questions.Enabled {
			return false, nil
		}
		var enabled bool
		if err := db.Pool().QueryRow(ctx, "SELECT tiku_charge_enabled FROM public.billing_settings WHERE id=true").Scan(&enabled); err != nil {
			return false, fault.New("service_unavailable", "无法读取题库收费开关")
		}
		return enabled, nil
	}, CacheScopeSecret: deriveKey(identityKey, "question-cache-scope"), ForkTimeout: cfg.Questions.Timeout})
	if err != nil {
		return nil, err
	}
	runtime.Questions = answers
	automaticAnswers.questions = answers
	stream := events.New(db.Pool())
	stream.SetLogger(logger)
	if err := stream.SetMaxStreams(cfg.Operations.MaxEventStreams); err != nil {
		return nil, err
	}
	stream.SetSessionCheck(identity.ValidateActor)
	runtime.Events = stream
	mux := http.NewServeMux()
	identity.Register(mux)
	// 管理端健康检查走带会话认证的 API 前缀；公网入口隐藏 /health/*。
	mux.HandleFunc("GET /admin/api/v1/health/live", func(w http.ResponseWriter, r *http.Request) {
		web.JSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /admin/api/v1/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Pool().Ping(r.Context()); err != nil {
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "服务暂未就绪")
			return
		}
		web.JSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
	})
	learn.Register(mux)
	book.Register(mux)
	answers.Register(mux)
	ops.Register(mux)
	stream.Register(mux)
	// 支付回调直接交给供应商验签处理，双端 API 则始终先经过独立会话认证。
	router := web.Router(mux)
	guarded := ops.Guard(router)
	runtime.HTTP.User.Business = identity.Middleware("user", guarded)
	runtime.HTTP.Admin.Business = identity.Middleware("admin", guarded)
	runtime.HTTP.Admin.Callbacks = router
	runtime.HTTP.User.Capabilities = runtime.capabilities
	runtime.HTTP.Admin.Capabilities = runtime.capabilities
	return runtime, nil
}
func deriveKey(master []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("ByebyeOCS:v1:" + purpose))
	return mac.Sum(nil)
}
func (r *Runtime) capabilities(ctx context.Context, audience, id string) map[string]any {
	body := httpapi.Capabilities(audience, id)
	if r.Auth == nil {
		return body
	}
	authEnabled := true
	methods := []string{}
	if audience == "admin" {
		methods = []string{"password", "totp"}
	} else if r.userLoginAvailable {
		methods = []string{"password"}
	}
	body["authentication"] = map[string]any{"enabled": authEnabled, "login_methods": methods}
	capability := r.Learning.Capabilities()
	features := body["features"].(map[string]bool)
	features["preferences"] = true
	features["scheduling"] = true
	features["wallet"] = true
	features["course_sync"] = capability.CourseRead
	features["task_execution"] = r.cfg.Learning.ExecutionEnabled && capability.ExecutorEnabled && capability.PlatformCheck && capability.SessionResolver
	capability.ExecutionEnabled = features["task_execution"]
	capability.CredentialVerify = r.userLoginAvailable
	features["tiku"] = r.cfg.Questions.Enabled
	features["shared_question_cache"] = r.cfg.Questions.Enabled && r.cfg.Questions.AllowSharedCache
	body["execution"] = capability
	taskTypes := make([]map[string]any, 0, 5)
	for _, item := range []struct{ id, name string }{{"video", "视频"}, {"audio", "音频"}, {"document", "文档/阅读"}, {"quiz", "测验"}, {"other", "其他"}} {
		supported := false
		for _, kind := range capability.TaskKinds {
			if kind == item.id {
				supported = true
				break
			}
		}
		taskTypes = append(taskTypes, map[string]any{"id": item.id, "name": item.name, "available": supported})
	}
	body["task_types"] = taskTypes
	readCtx, cancel := context.WithTimeout(ctx, r.cfg.Database.QueryTimeout)
	defer cancel()
	price, err := r.Ledger.PublicMetadata(readCtx)
	if err == nil {
		gateReady := r.Operations.Gate(readCtx) == nil
		features["payments"] = price.Recharge.Enabled && gateReady
		tikuConfigured, tikuAutomatic, tikuErr := r.Questions.PublicPricing(readCtx)
		if tikuErr != nil {
			tikuConfigured = false
			tikuAutomatic = false
		}
		body["pricing"] = map[string]bool{"service_configured": price.Service.Configured, "tiku_configured": tikuConfigured, "automatic_charging": (price.Service.State == "active" && price.Service.Configured || tikuAutomatic && tikuConfigured) && gateReady}
	}
	return body
}
func (r *Runtime) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wait sync.WaitGroup
	errors := make(chan error, 3)
	for _, run := range []func(context.Context) error{func(ctx context.Context) error {
		if r.Learning == nil {
			return nil
		}
		return r.Learning.Run(ctx)
	}, func(ctx context.Context) error {
		if r.Events == nil {
			return nil
		}
		return r.Events.Run(ctx)
	}, func(ctx context.Context) error {
		if r.Operations == nil {
			return nil
		}
		return r.Operations.Run(ctx)
	}} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := run(runCtx); err != nil {
				errors <- err
				cancel()
			}
		}()
	}
	err := ServeWithOptions(runCtx, r.cfg.Server, r.db, r.logger, r.HTTP)
	cancel()
	wait.Wait()
	select {
	case backgroundErr := <-errors:
		if err == nil {
			err = backgroundErr
		}
	default:
	}
	return err
}
