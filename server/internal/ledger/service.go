// Package ledger 提供 PostgreSQL 事务账本、钱包、价格、用量、充值订单与 ZPay 支付接入。
// 钱包缓存不是账本真相；所有资金变化必须在调用方或本包提供的显式事务内写入平衡分录。
package ledger

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/events"
	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

const (
	ScopeServiceTaskPoint = "service_task_point"
	ScopeTikuTokens       = "tiku_tokens"

	CurrencyCNY   = "CNY"
	ChannelAlipay = "alipay"
	ChannelWxpay  = "wxpay"

	// 管理权限字符串由认证服务分配；owner 的 "*" 同时满足这些权限。
	PermBillingRead   = "billing.read"
	PermBillingManage = "billing.manage"
	PermWalletAdjust  = "wallet.adjust"
	PermWalletRefund  = "wallet.refund"
	PermReconcile     = "reconciliation.manage"
)

const (
	systemChannelClearing    = "00000000-0000-4000-8000-000005000001"
	systemServiceRevenue     = "00000000-0000-4000-8000-000005000002"
	systemRefundPayable      = "00000000-0000-4000-8000-000005000003"
	systemPlatformAdjustment = "00000000-0000-4000-8000-000005000004"
)

// Options 由服务端入口在构造时提供；不从 HTTP 请求读取。
type Options struct {
	Now func() time.Time
	// Provider 为空表示支付未配置。生产二进制只能注入真实 ZPay；测试可注入受控模拟器。
	Provider payments.Provider
	// ZPay 非空且 Provider 为空时，由本包构造真实 ZPay 客户端。
	ZPay *payments.ZPayConfig
	// PageSize 是列表默认页大小，最大 100。
	PageSize int
	// ReconcileLimit 是单次对账最多扫描的钱包/订单数量。
	ReconcileLimit int
	// PaymentQueryTimeout 限制主动查单，网络调用不在账本事务内。
	PaymentQueryTimeout time.Duration
	// ChargingAllowed 是运营侧运行时授权。nil 表示沿用数据库开关（测试/受控调用）；
	// 主二进制应始终传入非 nil，默认 false。为 false 时不拒绝释放、退款和已付款回调。
	ChargingAllowed *bool
	// Gate 在真正新建预占、结算扣费或创建充值订单前执行，用于备份/磁盘新鲜度等前置检查。
	// 释放预占、异常退款、已支付回调和主动查单不经过 Gate。
	Gate func(context.Context) error
}

type Service struct {
	pool            *pgxpool.Pool
	provider        payments.Provider
	now             func() time.Time
	pageSize        int
	reconcileLimit  int
	queryTimeout    time.Duration
	chargingAllowed *bool
	gate            func(context.Context) error
	// 仅测试使用：在通知 Inbox 写入后、资金入账前注入中断，验证事务回滚与重试。
	testHook func(stage string) error
}

func New(pool *pgxpool.Pool, options Options) (*Service, error) {
	if pool == nil {
		return nil, fault.New("service_unavailable", "账本服务要求数据库连接池")
	}
	if options.Provider != nil && options.ZPay != nil {
		return nil, fault.New("validation_failed", "支付提供商与 ZPay 配置只能设置一个")
	}
	provider := options.Provider
	if provider == nil && options.ZPay != nil {
		zpay, err := payments.NewZPay(*options.ZPay)
		if err != nil {
			return nil, err
		}
		provider = zpay
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	pageSize := options.PageSize
	if pageSize == 0 {
		pageSize = 20
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, fault.New("validation_failed", "页大小必须在 1 到 100 之间")
	}
	reconcileLimit := options.ReconcileLimit
	if reconcileLimit == 0 {
		reconcileLimit = 10
	}
	if reconcileLimit < 1 || reconcileLimit > 100 {
		return nil, fault.New("validation_failed", "对账扫描上限必须在 1 到 100 之间")
	}
	queryTimeout := options.PaymentQueryTimeout
	if queryTimeout == 0 {
		queryTimeout = 8 * time.Second
	}
	if queryTimeout < time.Second || queryTimeout > 30*time.Second {
		return nil, fault.New("validation_failed", "支付查单超时必须在 1 到 30 秒之间")
	}
	var chargingAllowed *bool
	if options.ChargingAllowed != nil {
		value := *options.ChargingAllowed
		chargingAllowed = &value
	}
	return &Service{
		pool: pool, provider: provider, now: func() time.Time { return now().UTC() },
		pageSize: pageSize, reconcileLimit: reconcileLimit, queryTimeout: queryTimeout,
		chargingAllowed: chargingAllowed, gate: options.Gate,
	}, nil
}

func (s *Service) chargingAuthorized() bool {
	return s.chargingAllowed == nil || *s.chargingAllowed
}

// checkChargingGate 只用于真正的新收费/新支付操作；释放、退款和已付款回调不能调用它。
func (s *Service) checkChargingGate(ctx context.Context) error {
	if !s.chargingAuthorized() {
		return fault.New("not_configured", "资金收费尚未获得运营授权")
	}
	if s.gate == nil {
		return nil
	}
	if err := s.gate(ctx); err != nil {
		var safe *fault.Error
		if errors.As(err, &safe) {
			return err
		}
		return fault.New("service_unavailable", "资金操作前置检查未通过，请稍后重试")
	}
	return nil
}

// txRollbackTimeout 限制回滚等待时间。调用方上下文可能已被取消，回滚仍必须执行；
// 但网络失联时 pgx 会一直等待服务器应答，未加期限的回滚会占住连接池槽位。
// 超时后 pgx 关闭该连接，服务器在连接断开时回滚未提交事务；已提交分录与账本耐久性不变。
// 变量形式仅便于测试缩短等待，生产不修改。
var txRollbackTimeout = 5 * time.Second

// rollbackTx 在独立且有限的期限内回滚事务；已提交或已回滚的事务立即返回。
func rollbackTx(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), txRollbackTimeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func (s *Service) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fault.New("service_unavailable", "无法开启账本事务")
	}
	defer rollbackTx(tx)
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fault.New("service_unavailable", "账本事务提交结果不明，请先查询状态再重试")
	}
	return nil
}

func requireTx(tx pgx.Tx) error {
	if tx == nil {
		return fault.New("validation_failed", "账本方法要求调用方提供显式事务")
	}
	return nil
}

func digest(parts ...string) string {
	h := sha256.New()
	for i, part := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) requestID(ctx context.Context) string {
	if id := web.RequestID(ctx); id != "" {
		return id
	}
	return web.NewRequestID()
}

func (s *Service) audit(ctx context.Context, tx pgx.Tx, actor web.Actor, action, resourceType, resourceID, reason string) error {
	return audit.Record(ctx, tx, audit.Event{
		Actor: actor, Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Reason: reason, RequestID: s.requestID(ctx),
	})
}

func actorFromID(kind, id string) web.Actor {
	return web.Actor{Kind: kind, ID: id}
}

func requireAgent(actor web.Actor, kind, permission string) error {
	if actor.Kind != kind || actor.ID == "" || !web.ValidID(actor.ID) || !web.HasPermission(actor, permission) {
		return fault.New("forbidden", "当前身份无权执行此操作")
	}
	return nil
}

func requireFresh(actor web.Actor, now time.Time) error {
	if !web.Fresh(actor, now) {
		return fault.New("forbidden", "需要重新验证管理员身份后再执行此操作")
	}
	return nil
}

func validKey(value string) error {
	if len(strings.TrimSpace(value)) != len(value) || len(value) < 1 || len(value) > 180 {
		return fault.New("validation_failed", "业务键为空、包含首尾空白或超出 180 字符")
	}
	return nil
}

func newUUID() string { return web.NewID() }

func newMerchantOrderNo(now time.Time) (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fault.New("service_unavailable", "无法生成支付订单号")
	}
	// 14 位时间 + 15 位随机十进制，总长 29 位，不超过提供商 32 位上限。
	value := new(big.Int).SetBytes(random[:])
	return now.UTC().Format("20060102150405") + fmt.Sprintf("%015d", value), nil
}

func newProviderRefundNo(now time.Time) (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fault.New("service_unavailable", "无法生成退款编号")
	}
	value := new(big.Int).SetBytes(random[:])
	return "R" + now.UTC().Format("20060102150405") + fmt.Sprintf("%015d", value), nil
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23503"
}

func (s *Service) runHook(stage string) error {
	if s.testHook == nil {
		return nil
	}
	return s.testHook(stage)
}

// recordWalletEvent 在业务事务内写入最小钱包通知，不包含支付原文或凭据。
func (s *Service) recordWalletEvent(ctx context.Context, tx pgx.Tx, wallet *walletRow,
	eventType string, extra map[string]any) error {
	data := map[string]any{
		"available_minor":     formatAmount(wallet.AvailableMinor),
		"reserved_minor":      formatAmount(wallet.ReservedMinor),
		"tiku_accrued_micros": formatAmount(wallet.TikuAccruedMicros),
	}
	for key, value := range extra {
		data[key] = value
	}
	return events.Record(ctx, tx, events.Event{
		Audience: "user", UserID: wallet.UserID, Type: eventType,
		ResourceID: wallet.ID, Version: wallet.Version, Data: data,
	})
}
