package questions

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// offeringSyncLock 序列化目录镜像写入，避免并发同步争抢默认条目唯一索引。
const offeringSyncLock int64 = 734207071013

var offeringPublicIDPattern = regexp.MustCompile(`^qm_[0-9a-f]{32}$`)

// fetchCatalogue 从题库服务读取公开模型目录。条目格式不完整时整体拒绝，
// 不把缺失字段或可疑费率当成可售价格。
func (s *Service) fetchCatalogue(ctx context.Context) ([]*Offering, error) {
	if err := s.requireFork(); err != nil {
		return nil, err
	}
	timeout := s.timeout
	if timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	items, err := s.fork.Offerings(readCtx)
	if err != nil {
		return nil, fault.New("service_unavailable", "无法读取题库模型目录")
	}
	result := make([]*Offering, 0, len(items))
	for _, item := range items {
		offering, err := offeringFromFork(item)
		if err != nil {
			return nil, fault.New("service_unavailable", "题库公开模型条目格式无效")
		}
		result = append(result, offering)
	}
	return result, nil
}

func offeringFromFork(item ForkOffering) (*Offering, error) {
	if !web.ValidID(item.ID) || !offeringPublicIDPattern.MatchString(item.PublicID) {
		return nil, fault.New("validation_failed", "公开条目编号无效")
	}
	displayName := strings.TrimSpace(item.DisplayName)
	if displayName == "" || len([]rune(displayName)) > 80 {
		return nil, fault.New("validation_failed", "公开名称无效")
	}
	if len([]rune(item.Description)) > 500 || strings.TrimSpace(item.ProviderRef) == "" {
		return nil, fault.New("validation_failed", "公开条目说明或模型引用无效")
	}
	if err := item.Pricing.Validate(); err != nil {
		return nil, err
	}
	if item.Pricing != nil && item.Pricing.Version != item.PriceVersion {
		return nil, fault.New("validation_failed", "价格版本不一致")
	}
	if item.CachePolicy.Scope != "user" && item.CachePolicy.Scope != "shared" {
		return nil, fault.New("validation_failed", "缓存范围无效")
	}
	if item.CachePolicy.TTLDays < 1 || item.CachePolicy.TTLDays > 365 {
		return nil, fault.New("validation_failed", "缓存期限无效")
	}
	capabilities := item.Capabilities
	if len(capabilities) == 0 {
		capabilities = []string{"text"}
	}
	health := "unavailable"
	if item.Health == "configured" {
		health = "configured"
	}
	if item.Version < 1 || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
		return nil, fault.New("validation_failed", "公开条目版本无效")
	}
	if item.Default && !item.Enabled {
		return nil, fault.New("validation_failed", "默认条目必须可见")
	}
	return &Offering{
		ID: item.ID, PublicID: item.PublicID, DisplayName: displayName, Description: item.Description,
		ProviderRef: item.ProviderRef, Capabilities: capabilities, Enabled: item.Enabled, Default: item.Default,
		SortOrder: item.SortOrder, PriceVersion: item.PriceVersion, Pricing: item.Pricing,
		CachePolicy: item.CachePolicy, Health: health, Version: item.Version,
		CreatedAt: item.CreatedAt.UTC(), UpdatedAt: item.UpdatedAt.UTC(),
	}, nil
}

// syncCatalogue 把题库目录写入主库只读镜像：更新现有条目、写入新条目，
// 并把题库已移除的条目置为不可见；不删除历史行，避免查询外键和历史展示丢失。
func (s *Service) syncCatalogue(ctx context.Context, items []*Offering) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, offeringSyncLock); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE public.question_offerings SET is_default = false WHERE is_default`); err != nil {
		return err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if err := upsertOfferingRow(ctx, tx, item); err != nil {
			return err
		}
		ids = append(ids, item.ID)
	}
	if _, err := tx.Exec(ctx, `UPDATE public.question_offerings
		SET enabled = false, is_default = false, health = 'unavailable', version = version + 1, updated_at = now()
		WHERE NOT (id::text = ANY($1::text[]))`, ids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// syncOffering 在创建查询前同步本次使用的条目，保证主库外键行存在。
func (s *Service) syncOffering(ctx context.Context, item *Offering) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, offeringSyncLock); err != nil {
		return err
	}
	if item.Default {
		if _, err := tx.Exec(ctx, `UPDATE public.question_offerings SET is_default = false WHERE is_default AND id <> $1`, item.ID); err != nil {
			return err
		}
	}
	if err := upsertOfferingRow(ctx, tx, item); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertOfferingRow(ctx context.Context, tx pgx.Tx, item *Offering) error {
	capabilities, err := json.Marshal(item.Capabilities)
	if err != nil {
		return err
	}
	var pricing []byte
	if item.Pricing != nil {
		if pricing, err = json.Marshal(item.Pricing); err != nil {
			return err
		}
	}
	policy, err := json.Marshal(item.CachePolicy)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.question_offerings
		(id, public_id, display_name, description, provider_ref, capabilities, enabled, is_default, sort_order,
		 price_version, pricing, cache_policy, health, version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
			public_id = EXCLUDED.public_id, display_name = EXCLUDED.display_name, description = EXCLUDED.description,
			provider_ref = EXCLUDED.provider_ref, capabilities = EXCLUDED.capabilities, enabled = EXCLUDED.enabled,
			is_default = EXCLUDED.is_default, sort_order = EXCLUDED.sort_order, price_version = EXCLUDED.price_version,
			pricing = EXCLUDED.pricing, cache_policy = EXCLUDED.cache_policy, health = EXCLUDED.health,
			version = EXCLUDED.version, updated_at = EXCLUDED.updated_at`,
		item.ID, item.PublicID, item.DisplayName, item.Description, item.ProviderRef, capabilities,
		item.Enabled, item.Default, item.SortOrder, item.PriceVersion, pricing, policy, item.Health,
		item.Version, item.CreatedAt, item.UpdatedAt)
	return err
}
