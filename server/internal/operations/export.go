package operations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// ExportUserData 使用固定投影导出用户自己的事实，排除身份索引、会话、密钥和支付材料。
func (s *Service) ExportUserData(ctx context.Context, userID string) (map[string]any, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fault.New("service_unavailable", "暂时无法读取个人数据")
	}
	defer rollbackTx(tx)
	sections := []struct{ name, query string }{
		{"profile", `SELECT jsonb_build_object('id',id,'status',status,'created_at',created_at,'updated_at',updated_at) FROM public.users WHERE id=$1`},
		{"preferences", `SELECT jsonb_build_object('color_scheme',color_scheme,'theme_mode',theme_mode,'notifications',jsonb_build_object('task_attention',notify_task_attention,'wallet_changes',notify_wallet_changes),'version',version) FROM public.user_preferences WHERE user_id=$1`},
		{"identity_display", `SELECT jsonb_build_object('provider',provider,'display_name',display_name,'school',school,'verified_at',verified_at,'status',status) FROM public.external_identities WHERE user_id=$1`},
		{"courses", `SELECT jsonb_build_object('id',id,'title',title,'teacher',teacher,'term',term,'synced_at',synced_at,'version',version) FROM public.learning_courses WHERE user_id=$1 ORDER BY created_at,id`},
		{"plans", `SELECT jsonb_build_object('id',id,'name',name,'status',status,'priority',priority,'version',version,'created_at',created_at,'updated_at',updated_at) FROM public.learning_plans WHERE user_id=$1 ORDER BY created_at,id`},
		{"plan_revisions", `SELECT jsonb_build_object('id',id,'plan_id',plan_id,'version',version,'course_ids',course_ids,'scope_kind',scope_kind,'chapter_ids',chapter_ids,'task_types',task_types,'priority',priority,'question_policy',jsonb_build_object('enabled',question_policy_enabled,'model_offering_id',question_model_offering_id,'mode',question_mode,'require_manual_confirmation',require_manual_confirmation),'created_at',created_at) FROM public.learning_plan_revisions WHERE user_id=$1 ORDER BY created_at,id`},
		{"tasks", `SELECT jsonb_build_object('id',id,'course_id',course_id,'chapter_id',chapter_id,'title',title,'task_kind',task_kind,'state',state,'desired_state',desired_state,'version',version,'platform_status',platform_status,'platform_check',platform_check,'observation',observation,'media_evidence',media_evidence,'confirmed_at',confirmed_at,'billing_state',billing_state,'created_at',created_at,'updated_at',updated_at) FROM public.learning_tasks WHERE user_id=$1 ORDER BY created_at,id`},
		{"question_task_runs", `SELECT jsonb_build_object('id',id,'task_id',task_id,'plan_revision_id',plan_revision_id,'model_offering_id',model_offering_id,'answer_mode',answer_mode,'state',state,'query_ids',query_ids,'submitted_at',submitted_at,'created_at',created_at,'updated_at',updated_at) FROM public.learning_work_runs WHERE user_id=$1 ORDER BY created_at,id`},
		{"wallet", `SELECT jsonb_build_object('id',id,'currency',currency,'available_minor',available_minor::text,'reserved_minor',reserved_minor::text,'tiku_accrued_micros',tiku_accrued_micros::text,'version',version) FROM public.wallets WHERE user_id=$1`},
		{"wallet_entries", `SELECT jsonb_build_object('id',id,'kind',kind,'currency',currency,'amount_minor',amount_minor::text,'reason',reason,'reference_type',reference_type,'reference_id',reference_id,'created_at',created_at) FROM public.journals WHERE user_id=$1 ORDER BY created_at,id`},
		{"orders", `SELECT jsonb_build_object('id',id,'amount_minor',amount_minor::text,'currency',currency,'channel',channel,'status',status,'payment_status',payment_status,'credit_status',credit_status,'created_at',created_at,'paid_at',paid_at,'credited_at',credited_at) FROM public.recharge_orders WHERE user_id=$1 ORDER BY created_at,id`},
		{"usage", `SELECT jsonb_build_object('id',id,'scope',scope,'status',status,'quantity_scaled',quantity_scaled::text,'input_tokens',input_tokens::text,'output_tokens',output_tokens::text,'total_tokens',total_tokens::text,'usage_complete',usage_complete,'price_version_id',price_version_id,'cost_micros',cost_micros::text,'charged_minor',charged_minor::text) FROM public.usage_records WHERE user_id=$1 ORDER BY created_at,id`},
		{"question_queries", `SELECT jsonb_build_object('id',id,'model_offering_id',offering_public_id,'model_display_name',offering_display_name,'mode',mode,'question_title',question_title,'question_options',question_options,'question_type',question_type,'answer',answer,'status',status,'cache_hit',cache_hit,'usage',usage,'pricing',pricing,'created_at',created_at,'completed_at',completed_at) FROM public.question_queries WHERE user_id=$1 ORDER BY created_at,id`},
	}
	result := map[string]any{"format_version": "1", "generated_at": time.Now().UTC(), "user_id": userID}
	size := int64(0)
	for _, section := range sections {
		rows, err := tx.Query(ctx, section.query, userID)
		if err != nil {
			return nil, fault.New("service_unavailable", "个人数据导出中断，请稍后重试")
		}
		items := []json.RawMessage{}
		for rows.Next() {
			var data []byte
			if rows.Scan(&data) != nil {
				rows.Close()
				return nil, fault.New("service_unavailable", "个人数据暂不可导出")
			}
			size += int64(len(data))
			if size > s.options.ExportMaxBytes {
				rows.Close()
				return nil, fault.New("export_too_large", "数据超过在线导出上限，需要人工处理")
			}
			items = append(items, json.RawMessage(data))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fault.New("service_unavailable", "个人数据读取中断")
		}
		result[section.name] = items
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fault.New("service_unavailable", "导出快照未完成")
	}
	return result, nil
}
