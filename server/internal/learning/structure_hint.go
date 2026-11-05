package learning

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// structureHintMaxAge 限制结构指纹的复用期限；超过后按完整发现重读，避免长期依赖旧结构。
const structureHintMaxAge = 7 * 24 * 60 * 60 // 秒

// courseStructureHint 读取上一版快照的结构指纹：平台课程级修改时间与各章节卡片页码。
// 任何一项缺失或读取失败都返回空指纹，调用方退化为完整发现，不影响同步本身。
func (s *Service) courseStructureHint(ctx context.Context, userID, externalCourseID, externalClassID string) CourseStructureHint {
	var courseID string
	var version, modifiedAt int64
	err := s.pool.QueryRow(ctx, `SELECT c.id::text, c.snapshot_version, snapshot.platform_modified_at
		FROM public.learning_courses c
		JOIN public.learning_course_snapshots snapshot
		  ON snapshot.course_id = c.id AND snapshot.user_id = c.user_id AND snapshot.version = c.snapshot_version
		WHERE c.user_id = $1 AND c.external_course_id = $2 AND c.external_class_id = $3
		  AND c.cycle_key = 'current' AND c.snapshot_version > 0 AND snapshot.platform_modified_at > 0
		  AND snapshot.captured_at > now() - make_interval(secs => $4)`,
		userID, externalCourseID, externalClassID, structureHintMaxAge).
		Scan(&courseID, &version, &modifiedAt)
	if err != nil {
		if err != pgx.ErrNoRows {
			return CourseStructureHint{}
		}
		return CourseStructureHint{}
	}
	// 没有附件的章节也要出现在指纹里：缺键代表新章节，空列表代表已知但无任务点。
	rows, err := s.pool.Query(ctx, `SELECT chapter.external_id, COALESCE(attachment.card_index, -1)
		FROM public.learning_chapters chapter
		LEFT JOIN public.learning_attachments attachment
		  ON attachment.chapter_id = chapter.id AND attachment.snapshot_version = chapter.snapshot_version
		WHERE chapter.course_id = $1 AND chapter.user_id = $2 AND chapter.snapshot_version = $3
		GROUP BY chapter.external_id, attachment.card_index
		ORDER BY chapter.external_id, attachment.card_index`, courseID, userID, version)
	if err != nil {
		return CourseStructureHint{}
	}
	defer rows.Close()
	indexes := map[string][]int{}
	for rows.Next() {
		var externalID string
		var cardIndex int
		if err := rows.Scan(&externalID, &cardIndex); err != nil {
			return CourseStructureHint{}
		}
		if _, exists := indexes[externalID]; !exists {
			indexes[externalID] = []int{}
		}
		if cardIndex >= 0 {
			indexes[externalID] = append(indexes[externalID], cardIndex)
		}
	}
	if rows.Err() != nil || len(indexes) == 0 {
		return CourseStructureHint{}
	}
	return CourseStructureHint{ModifiedAt: modifiedAt, Indexes: indexes}
}
