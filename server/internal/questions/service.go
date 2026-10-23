// Package questions 提供主平台公开模型目录、题库查询、缓存报告与受控平台提交。
//
// 上游题库是独立 fork 服务；本包只保存公开条目和查询/计费事实，不保存上游地址、模型名或密钥。
package questions

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// Micros 以十进制字符串在 API 中传输，避免 JSON 浮点或 64 位整数精度丢失。
type Micros int64

func (m Micros) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(m), 10) + `"`), nil
}

func (m *Micros) UnmarshalJSON(data []byte) error {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return errors.New("金额必须是十进制字符串")
	}
	value, err := strconv.ParseInt(string(data[1:len(data)-1]), 10, 64)
	if err != nil || value < 0 {
		return errors.New("金额必须是非负十进制字符串")
	}
	*m = Micros(value)
	return nil
}

// OfferingPricing 是管理员为单个公开模型发布的不可变价格快照。
// 费率单位为微元/百万 token；显式 0 表示免费，nil 表示未定价。
type OfferingPricing struct {
	Version                      string `json:"version"`
	InputMicrosPerMillionTokens  Micros `json:"input_micros_per_million_tokens"`
	OutputMicrosPerMillionTokens Micros `json:"output_micros_per_million_tokens"`
	MicrosPerMinor               Micros `json:"micros_per_minor"`
	Rounding                     string `json:"rounding"`
	Rule                         string `json:"rule"`
}

func (p *OfferingPricing) Validate() error {
	if p == nil {
		return nil
	}
	if strings.TrimSpace(p.Version) == "" || len(p.Version) > 64 {
		return fault.New("validation_failed", "价格版本不能为空或超过 64 字")
	}
	if strings.TrimSpace(p.Rule) == "" || len(p.Rule) > 64 {
		return fault.New("validation_failed", "结算规则不能为空或超过 64 字")
	}
	if p.Rounding != "ceil" && p.Rounding != "floor" {
		return fault.New("validation_failed", "价格 rounding 必须是 ceil 或 floor")
	}
	if p.MicrosPerMinor != 10000 {
		return fault.New("validation_failed", "人民币每分固定为10000微元")
	}
	if p.InputMicrosPerMillionTokens < 0 || p.OutputMicrosPerMillionTokens < 0 {
		return fault.New("validation_failed", "token 费率不能为负")
	}
	return nil
}

func (p *OfferingPricing) Free() bool {
	return p != nil && p.InputMicrosPerMillionTokens == 0 && p.OutputMicrosPerMillionTokens == 0
}

type PublicPricing struct {
	Version                      string `json:"version"`
	Rule                         string `json:"rule"`
	Rounding                     string `json:"rounding"`
	Unit                         string `json:"unit"`
	InputMicrosPerMillionTokens  Micros `json:"input_micros_per_million_tokens"`
	OutputMicrosPerMillionTokens Micros `json:"output_micros_per_million_tokens"`
}

func (p *OfferingPricing) Public() *PublicPricing {
	if p == nil {
		return nil
	}
	return &PublicPricing{Version: p.Version, Rule: p.Rule, Rounding: p.Rounding,
		Unit: "micros_per_million_tokens", InputMicrosPerMillionTokens: p.InputMicrosPerMillionTokens,
		OutputMicrosPerMillionTokens: p.OutputMicrosPerMillionTokens}
}

type Options struct {
	Fork    ForkClient
	Billing Billing
	// AutomaticCharging 是部署级真实扣费门槛；关闭时仍可展示价格，但不预占或扣费。
	AutomaticCharging bool
	// AutomaticChargingAllowed 可选；每次新查询在预占前读取并冻结结果。
	// nil 时保持 AutomaticCharging 的静态语义。
	AutomaticChargingAllowed func(context.Context) (bool, error)
	MaxInputTokens           int
	// 预估上界额外计入每张图片的输入/转述输出 token，避免图片题预占不足。
	ImageInputTokens            int
	CaptionOutputTokensPerImage int
	CacheScopeSecret            []byte
	AllowSharedCache            bool
	MaxOutputTokens             int
	MaxImages                   int
	MaxImageBytes               int64
	MaxImagePixels              int64
	MaxRequestBytes             int64
	ForkTimeout                 time.Duration
	Now                         func() time.Time
}

// Billing 由账本模块实现；三个方法都必须在调用方事务内执行。
type Billing interface {
	Reserve(ctx context.Context, tx pgx.Tx, userID, key string, amountMinor int64) (holdID string, err error)
	Settle(ctx context.Context, tx pgx.Tx, holdID, key string, actualMinor int64) error
	Release(ctx context.Context, tx pgx.Tx, holdID, key string) error
}

type Service struct {
	pool          *pgxpool.Pool
	fork          ForkClient
	billing       Billing
	auto          bool
	autoGate      func(context.Context) (bool, error)
	maxInput      int
	imageInput    int
	captionOutput int
	scopeKey      []byte
	shared        bool
	maxOut        int
	maxImg        int
	maxBytes      int64
	maxPix        int64
	maxBody       int64
	timeout       time.Duration
	now           func() time.Time
}

func New(pool *pgxpool.Pool, options Options) (*Service, error) {
	if pool == nil {
		return nil, fault.New("service_unavailable", "题库服务要求数据库连接池")
	}
	secret := options.CacheScopeSecret
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fault.New("service_unavailable", "无法初始化缓存范围密钥")
		}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	maxOut := options.MaxOutputTokens
	if maxOut == 0 {
		maxOut = 2048
	}
	maxImages := options.MaxImages
	if maxImages == 0 {
		maxImages = 8
	}
	maxBytes := options.MaxImageBytes
	if maxBytes == 0 {
		maxBytes = 5 << 20
	}
	maxPixels := options.MaxImagePixels
	if maxPixels == 0 {
		maxPixels = 16000000
	}
	maxBody := options.MaxRequestBytes
	if maxBody == 0 {
		maxBody = 30 << 20
	}
	if maxBody < 1<<20 || maxBody > 32<<20 {
		return nil, fault.New("validation_failed", "题库请求体上限无效")
	}
	timeout := options.ForkTimeout
	if timeout == 0 {
		timeout = 95 * time.Second
	}
	maxInput := options.MaxInputTokens
	if maxInput == 0 {
		maxInput = 32768
	}
	if maxInput < 1 || maxInput > 1_000_000 {
		return nil, fault.New("validation_failed", "题库输入 token 上限无效")
	}
	imageInput := options.ImageInputTokens
	if imageInput == 0 {
		imageInput = 2048
	}
	captionOutput := options.CaptionOutputTokensPerImage
	if captionOutput == 0 {
		captionOutput = 1024
	}
	if imageInput < 0 || captionOutput < 0 {
		return nil, fault.New("validation_failed", "图片 token 预算无效")
	}
	if maxOut < 1 || maxOut > 32768 {
		return nil, fault.New("validation_failed", "题库输出预算上限无效")
	}
	if maxImages < 1 || maxImages > 32 {
		return nil, fault.New("validation_failed", "题库图片数量上限无效")
	}
	if maxBytes < 1 || maxPixels < 1 {
		return nil, fault.New("validation_failed", "题库图片限制无效")
	}
	if timeout < time.Second {
		return nil, fault.New("validation_failed", "题库服务超时过短")
	}
	return &Service{
		pool: pool, fork: options.Fork, billing: options.Billing,
		auto: options.AutomaticCharging, autoGate: options.AutomaticChargingAllowed, maxInput: maxInput,
		imageInput: imageInput, captionOutput: captionOutput,
		scopeKey: secret, shared: options.AllowSharedCache,
		maxOut: maxOut, maxImg: maxImages, maxBytes: maxBytes, maxPix: maxPixels, maxBody: maxBody,
		timeout: timeout, now: func() time.Time { return now().UTC() },
	}, nil
}

// Register 注册完整的用户与管理员路径。入口也可分别使用 RegisterUser/RegisterAdmin，
// 避免把管理员路由挂到用户监听。
func (s *Service) Register(mux *http.ServeMux) {
	s.RegisterUser(mux)
	s.RegisterAdmin(mux)
}

func (s *Service) RegisterUser(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/question-models", s.listPublicOfferings)
	mux.HandleFunc("POST /api/v1/question-queries", s.createQuery)
	mux.HandleFunc("GET /api/v1/question-queries", s.listUserQueries)
	mux.HandleFunc("GET /api/v1/question-queries/{id}", s.getUserQuery)
	mux.HandleFunc("POST /api/v1/question-cache-reports", s.createCacheReport)
}

func (s *Service) RegisterAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/v1/question-models", s.adminListOfferings)
	mux.HandleFunc("POST /admin/api/v1/question-models", s.adminCreateOffering)
	mux.HandleFunc("PATCH /admin/api/v1/question-models/{id}", s.adminPatchOffering)
	mux.HandleFunc("POST /admin/api/v1/question-models/{id}/tests", s.adminTestOffering)
	mux.HandleFunc("GET /admin/api/v1/question-usage", s.adminUsage)
	mux.HandleFunc("GET /admin/api/v1/question-queries", s.adminListQueries)
	mux.HandleFunc("GET /admin/api/v1/question-cache-reports", s.adminListCacheReports)
	mux.HandleFunc("POST /admin/api/v1/question-cache-reports/{id}/review", s.adminReviewCacheReport)
	mux.HandleFunc("POST /admin/api/v1/question-cache/invalidations", s.adminInvalidateCache)
}

type QueryInput struct {
	ModelOfferingID string `json:"model_offering_id"`
	Mode            string `json:"mode"`
	CacheScope      string `json:"cache_scope"`
	Question        struct {
		Title   string   `json:"title"`
		Options string   `json:"options"`
		Type    string   `json:"type"`
		Images  []string `json:"images"`
	} `json:"question"`
	Budget struct {
		MaxOutputTokens int `json:"max_output_tokens"`
	} `json:"budget"`
}

type CachePolicy struct {
	Allow   bool   `json:"allow"`
	Scope   string `json:"scope"`
	TTLDays int    `json:"ttl_days"`
}

type Offering struct {
	ID           string
	PublicID     string
	DisplayName  string
	Description  string
	ProviderRef  string
	Capabilities []string
	Enabled      bool
	Default      bool
	SortOrder    int
	PriceVersion string
	Pricing      *OfferingPricing
	CachePolicy  CachePolicy
	Health       string
	Version      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type publicOfferingView struct {
	ID           string         `json:"id"`
	OfferingID   string         `json:"offering_id"`
	DisplayName  string         `json:"display_name"`
	Description  string         `json:"description"`
	Capabilities []string       `json:"capabilities"`
	Default      bool           `json:"default"`
	SortOrder    int            `json:"sort_order"`
	PriceVersion string         `json:"price_version"`
	Pricing      *PublicPricing `json:"pricing"`
	CachePolicy  CachePolicy    `json:"cache_policy"`
	Health       string         `json:"health"`
}

type adminOfferingView struct {
	ID           string           `json:"id"`
	OfferingID   string           `json:"offering_id"`
	DisplayName  string           `json:"display_name"`
	Description  string           `json:"description"`
	ProviderRef  string           `json:"provider_ref"`
	Capabilities []string         `json:"capabilities"`
	Enabled      bool             `json:"enabled"`
	Default      bool             `json:"default"`
	SortOrder    int              `json:"sort_order"`
	PriceVersion string           `json:"price_version"`
	Pricing      *OfferingPricing `json:"pricing"`
	CachePolicy  CachePolicy      `json:"cache_policy"`
	Health       string           `json:"health"`
	Version      int64            `json:"version"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

func (o Offering) Public() publicOfferingView {
	return publicOfferingView{ID: o.PublicID, OfferingID: o.PublicID, DisplayName: o.DisplayName, Description: o.Description,
		Capabilities: o.Capabilities, Default: o.Default, SortOrder: o.SortOrder,
		PriceVersion: o.PriceVersion, Pricing: o.Pricing.Public(), CachePolicy: o.CachePolicy, Health: o.Health}
}

func (o Offering) Admin() adminOfferingView {
	return adminOfferingView{ID: o.PublicID, OfferingID: o.PublicID, DisplayName: o.DisplayName, Description: o.Description,
		ProviderRef: o.ProviderRef, Capabilities: o.Capabilities, Enabled: o.Enabled, Default: o.Default,
		SortOrder: o.SortOrder, PriceVersion: o.PriceVersion, Pricing: o.Pricing, CachePolicy: o.CachePolicy,
		Health: o.Health, Version: o.Version, CreatedAt: o.CreatedAt.UTC(), UpdatedAt: o.UpdatedAt.UTC()}
}

func newPrefixedID(prefix string) string {
	return prefix + strings.ReplaceAll(web.NewID(), "-", "")
}

func validQuestionType(value string) bool {
	switch value {
	case "single", "multiple", "judgement", "completion", "shortanswer":
		return true
	}
	return false
}

func (s *Service) scopeKeyFor(userID string) string {
	mac := hmac.New(sha256.New, s.scopeKey)
	mac.Write([]byte("byebyeocs:question-cache-scope:" + userID))
	return hex.EncodeToString(mac.Sum(nil))
}

func fingerprint(userID string, input QueryInput) string {
	data, _ := json.Marshal(struct {
		User  string     `json:"user"`
		Input QueryInput `json:"input"`
	}{User: userID, Input: input})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type imageValidation struct {
	Count  int
	Digest string
}

func validateImages(title, options string, images []string, maxImages int, maxBytes, maxPixels int64) (imageValidation, error) {
	if len(images) > maxImages {
		return imageValidation{}, fault.New("validation_failed", fmt.Sprintf("图片数量不能超过 %d 张", maxImages))
	}
	markers := regexp.MustCompile(`\[图片([1-9][0-9]*)\]`).FindAllStringSubmatch(title+"\n"+options, -1)
	for _, marker := range markers {
		var index int
		_, _ = fmt.Sscanf(marker[1], "%d", &index)
		if index < 1 || index > len(images) {
			return imageValidation{}, fault.New("validation_failed", "图片标记缺少对应图片数据")
		}
	}
	// [图片N] 只做定位校验；自动做题的题干来自平台 HTML，图片按 DOM 顺序附加，
	// 不含占位标记。要求每张图片都被文本引用会把合法的平台图片题拒绝在模型调用前。
	hash := sha256.New()
	for i, raw := range images {
		header, encoded, ok := strings.Cut(raw, ",")
		if !ok || !strings.HasPrefix(strings.ToLower(header), "data:image/") || !strings.HasSuffix(strings.ToLower(header), ";base64") {
			return imageValidation{}, fault.New("validation_failed", "图片必须使用 Base64 data URL")
		}
		if int64(len(encoded)) > (maxBytes/3+1)*4+4 {
			return imageValidation{}, fault.New("validation_failed", "图片超过体积上限")
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || int64(len(data)) > maxBytes {
			return imageValidation{}, fault.New("validation_failed", "图片数据无效或超过体积上限")
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
			return imageValidation{}, fault.New("validation_failed", "图片格式或像素不符合要求")
		}
		fmt.Fprintf(hash, "%d:%d:", len(data), i+1)
		hash.Write(data)
	}
	return imageValidation{Count: len(images), Digest: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (s *Service) validateQuery(input *QueryInput) (imageValidation, error) {
	if input.Mode == "" {
		input.Mode = "cache_first"
	}
	if input.Mode != "cache_first" && input.Mode != "always_llm" {
		return imageValidation{}, fault.New("validation_failed", "作答方式无效")
	}
	if input.CacheScope == "" {
		input.CacheScope = "user"
	}
	if input.CacheScope != "user" && input.CacheScope != "shared" {
		return imageValidation{}, fault.New("validation_failed", "缓存范围无效")
	}
	if input.CacheScope == "shared" && !s.shared {
		return imageValidation{}, fault.New("validation_failed", "共享缓存尚未启用")
	}
	if input.Question.Type == "" {
		input.Question.Type = "single"
	}
	if !validQuestionType(input.Question.Type) {
		return imageValidation{}, fault.New("validation_failed", "题目类型无效")
	}
	if strings.TrimSpace(input.Question.Title) == "" || len(input.Question.Title) > 20000 || len(input.Question.Options) > 50000 {
		return imageValidation{}, fault.New("validation_failed", "题目内容为空或过长")
	}
	if input.Budget.MaxOutputTokens < 0 || input.Budget.MaxOutputTokens > s.maxOut {
		return imageValidation{}, fault.New("validation_failed", fmt.Sprintf("输出预算必须在 0 到 %d 之间", s.maxOut))
	}
	if input.Budget.MaxOutputTokens == 0 {
		input.Budget.MaxOutputTokens = 1024
		if input.Budget.MaxOutputTokens > s.maxOut {
			input.Budget.MaxOutputTokens = s.maxOut
		}
	}
	return validateImages(input.Question.Title, input.Question.Options, input.Question.Images, s.maxImg, s.maxBytes, s.maxPix)
}

// Quote 是价格计算事实；状态区分未定价、明确免费和已报价。
type Quote struct {
	State        string
	Pricing      *OfferingPricing
	InputTokens  int64
	OutputTokens int64
	CostMicros   int64
	AmountMinor  int64
	Estimated    bool
	// ChargingAllowed 是预占前冻结的运营开关决策；finalize 不再重新读取。
	ChargingAllowed   bool
	ChargingCheckedAt time.Time
}

func divRound(value *big.Int, divisor int64, rounding string) (*big.Int, error) {
	if divisor <= 0 {
		return nil, errors.New("价格精度无效")
	}
	q, r := new(big.Int).QuoRem(value, big.NewInt(divisor), new(big.Int))
	if r.Sign() != 0 {
		if rounding == "ceil" {
			q.Add(q, big.NewInt(1))
		} else if rounding != "floor" {
			return nil, errors.New("价格 rounding 无效")
		}
	}
	return q, nil
}

// priceUsage 用微元/百万 token 整数费率计算；乘除使用 big.Int，避免精度和溢出。
func priceUsage(pricing *OfferingPricing, inputTokens, outputTokens int64, overrideRounding string) (Quote, error) {
	quote := Quote{State: "unpriced", Pricing: pricing, InputTokens: inputTokens, OutputTokens: outputTokens}
	if pricing == nil {
		return quote, nil
	}
	if err := pricing.Validate(); err != nil {
		return quote, err
	}
	if inputTokens < 0 || outputTokens < 0 {
		return quote, errors.New("token 数无效")
	}
	rounding := pricing.Rounding
	if overrideRounding != "" {
		rounding = overrideRounding
	}
	numerator := new(big.Int).Mul(big.NewInt(inputTokens), big.NewInt(int64(pricing.InputMicrosPerMillionTokens)))
	numerator.Add(numerator, new(big.Int).Mul(big.NewInt(outputTokens), big.NewInt(int64(pricing.OutputMicrosPerMillionTokens))))
	micros, err := divRound(numerator, 1_000_000, rounding)
	if err != nil || !micros.IsInt64() {
		return quote, errors.New("价格计算溢出")
	}
	minor, err := divRound(micros, int64(pricing.MicrosPerMinor), rounding)
	if err != nil || !minor.IsInt64() {
		return quote, errors.New("价格换算溢出")
	}
	quote.CostMicros = micros.Int64()
	quote.AmountMinor = minor.Int64()
	if pricing.Free() || quote.CostMicros == 0 {
		quote.State = "free"
	} else {
		quote.State = "quoted"
	}
	return quote, nil
}

func (s *Service) estimateQuote(offering *Offering, outputBudget, imageCount int) (Quote, error) {
	// 预留按服务端文本输入上限、图片上界和用户预算内的输出上限计算，且向上取整。
	// 实际结算按服务端 token 事实与发布条目的 rounding 重新计算。
	inputBound := int64(s.maxInput) + int64(imageCount)*int64(s.imageInput)
	outputBound := int64(outputBudget) + int64(imageCount)*int64(s.captionOutput)
	return priceUsage(offering.Pricing, inputBound, outputBound, "ceil")
}

func (s *Service) resolveChargingAllowed(ctx context.Context) (bool, error) {
	if s.autoGate != nil {
		return s.autoGate(ctx)
	}
	return s.auto, nil
}

// PublicPricing 返回能力发现所需的公开布尔元数据，不要求管理员身份。
// configured 依据已发布且可用的模型自身 pricing，而不是遗留全局单价。
func (s *Service) PublicPricing(ctx context.Context) (configured bool, automatic bool, err error) {
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM public.question_offerings
		WHERE enabled AND health <> 'unavailable' AND pricing IS NOT NULL
	)`).Scan(&configured); err != nil {
		return false, false, fault.New("service_unavailable", "无法读取题库价格状态")
	}
	automatic, err = s.resolveChargingAllowed(ctx)
	if err != nil {
		return configured, false, fault.New("service_unavailable", "无法读取题库收费开关")
	}
	return configured, automatic, nil
}

func (s *Service) actualQuote(offering *Offering, usage Usage) (Quote, error) {
	if usage.InputTokens == nil || usage.OutputTokens == nil {
		return Quote{State: "usage_incomplete", Pricing: offering.Pricing}, nil
	}
	return priceUsage(offering.Pricing, *usage.InputTokens, *usage.OutputTokens, "")
}

func (s *Service) requireFork() error {
	if s.fork == nil {
		return fault.New("not_configured", "题库服务尚未配置")
	}
	return nil
}

func (s *Service) requireBilling() bool {
	return s.billing != nil
}

func (s *Service) billingKey(queryID string) string { return "question-query:" + queryID }

// pageCursor 是有界游标，不包含题目或用户敏感信息。
func encodeCursor(created time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(created.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(raw string) (time.Time, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", fault.New("validation_failed", "分页游标无效")
	}
	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fault.New("validation_failed", "分页游标无效")
	}
	created, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil || parts[1] == "" {
		return time.Time{}, "", fault.New("validation_failed", "分页游标无效")
	}
	return created, parts[1], nil
}

func (s *Service) pageLimit(r *http.Request) (int, error) {
	return web.Limit(r)
}
