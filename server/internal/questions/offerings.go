package questions

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 公开模型条目由题库服务发布，本服务只读。主库保留同步镜像，用于查询外键、
// 历史展示与用量汇总；价格以每次查询冻结的快照为准。

func (s *Service) listPublicOfferings(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "user", "self"); !ok {
		return
	}
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	sortAfter, idAfter := -1<<31, ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		sortAfter, idAfter, err = decodeOfferingCursor(raw)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
	}
	items, err := s.fetchCatalogue(r.Context())
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库模型列表")
		return
	}
	visible := make([]*Offering, 0, len(items))
	for _, item := range items {
		if item.Enabled && item.Health != "unavailable" {
			visible = append(visible, item)
		}
	}
	sortOfferings(visible)
	start := 0
	for start < len(visible) {
		item := visible[start]
		if item.SortOrder > sortAfter || (item.SortOrder == sortAfter && item.PublicID > idAfter) {
			break
		}
		start++
	}
	page := visible[start:]
	cursor := ""
	if len(page) > limit {
		page = page[:limit]
		last := page[len(page)-1]
		cursor = encodeOfferingCursor(last.SortOrder, last.PublicID)
	}
	views := make([]publicOfferingView, 0, len(page))
	for _, item := range page {
		views = append(views, item.Public())
	}
	var next any
	if cursor != "" {
		next = cursor
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": views, "next_cursor": next})
}

func (s *Service) adminListOfferings(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "question:read"); !ok {
		return
	}
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	sortAfter, idAfter := -1<<31, ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		sortAfter, idAfter, err = decodeOfferingCursor(raw)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
	}
	items, err := s.fetchCatalogue(r.Context())
	if err == nil {
		if err := s.syncCatalogue(r.Context(), items); err != nil {
			web.Error(w, r, 503, "service_unavailable", "无法同步题库模型列表")
			return
		}
	} else {
		// 管理员只读展示可以使用已同步镜像；题目报价路径仍以题库目录为准。
		items, err = s.mirrorOfferings(r.Context())
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "无法读取题库模型列表")
			return
		}
	}
	sortOfferings(items)
	start := 0
	for start < len(items) {
		item := items[start]
		if item.SortOrder > sortAfter || (item.SortOrder == sortAfter && item.PublicID > idAfter) {
			break
		}
		start++
	}
	page := items[start:]
	cursor := ""
	if len(page) > limit {
		page = page[:limit]
		last := page[len(page)-1]
		cursor = encodeOfferingCursor(last.SortOrder, last.PublicID)
	}
	views := make([]adminOfferingView, 0, len(page))
	for _, item := range page {
		views = append(views, item.Admin())
	}
	var next any
	if cursor != "" {
		next = cursor
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": views, "next_cursor": next})
}

func sortOfferings(items []*Offering) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SortOrder != items[j].SortOrder {
			return items[i].SortOrder < items[j].SortOrder
		}
		return items[i].PublicID < items[j].PublicID
	})
}

// defaultOffering 从题库目录中选择默认条目；未发布时拒绝查询。
func (s *Service) defaultOffering(ctx context.Context) (*Offering, error) {
	items, err := s.fetchCatalogue(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Default && item.Enabled && item.Health != "unavailable" {
			return item, nil
		}
	}
	return nil, fault.New("not_configured", "管理员尚未发布默认模型")
}

func (s *Service) offeringByPublicID(ctx context.Context, publicID string, includeDisabled bool) (*Offering, error) {
	items, err := s.fetchCatalogue(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.PublicID != publicID {
			continue
		}
		if !includeDisabled && (!item.Enabled || item.Health == "unavailable") {
			return nil, fault.New("not_found", "模型条目不存在或不可用")
		}
		return item, nil
	}
	return nil, fault.New("not_found", "模型条目不存在或不可用")
}

func encodeOfferingCursor(sortOrder int, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", sortOrder, id)))
}

func decodeOfferingCursor(raw string) (int, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, "", fault.New("validation_failed", "分页游标无效")
	}
	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return 0, "", fault.New("validation_failed", "分页游标无效")
	}
	value, err := strconv.Atoi(parts[0])
	if err != nil || parts[1] == "" {
		return 0, "", fault.New("validation_failed", "分页游标无效")
	}
	return value, parts[1], nil
}
