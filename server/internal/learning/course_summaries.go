package learning

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// saveCourseSummaries 先发布完整课程列表，不把缺失章节的概要伪装成完整快照。
// 旧快照及任务引用保留，结构不兼容时只报告失败。
func (s *Service) saveCourseSummaries(ctx context.Context, job *courseSyncJob, account accountRecord, courses []PlatformCourse) error {
	now := s.now()
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT u.status='active' AND ac.blocked_code=''
			AND (ac.blocked_until IS NULL OR ac.blocked_until<=$2)
			FROM public.users u JOIN public.learning_accounts ac ON ac.user_id=u.id
			WHERE u.id=$1 FOR SHARE OF u`, account.UserID, now).Scan(&allowed); err != nil {
			return errServiceUnavailable
		}
		if !allowed {
			return errLeaseLost
		}
		if err := tx.QueryRow(ctx, `SELECT fence_token=$2 AND status='running' AND lease_expires_at>$3
			FROM public.learning_course_syncs WHERE id=$1 FOR UPDATE`, job.ID, job.FenceToken, now).Scan(&allowed); err != nil {
			return errServiceUnavailable
		}
		if !allowed {
			return errLeaseLost
		}
		batch := &pgx.Batch{}
		for _, course := range courses {
			batch.Queue(`INSERT INTO public.learning_courses
				(id,user_id,provider,identity_namespace,identity_fingerprint,external_course_id,
				 external_class_id,external_cpi,cycle_key,title,teacher,term,updated_at,cover_source_url)
				VALUES ($1,$2,'chaoxing',$3,$4,$5,$6,$7,'current',COALESCE(NULLIF($8,''),$5),$9,$10,$11,$12)
				ON CONFLICT (user_id,provider,external_course_id,external_class_id,cycle_key)
				DO UPDATE SET title=COALESCE(NULLIF($8,''),learning_courses.title),
				 teacher=COALESCE(NULLIF($9,''),learning_courses.teacher),
				 term=COALESCE(NULLIF($10,''),learning_courses.term), external_cpi=EXCLUDED.external_cpi,
				 cover_source_url=CASE WHEN $13 THEN EXCLUDED.cover_source_url ELSE learning_courses.cover_source_url END,
				 updated_at=EXCLUDED.updated_at, version=learning_courses.version+1`,
				newID(), account.UserID, account.Namespace, account.Fingerprint,
				course.ExternalCourseID, course.ExternalClassID, course.CPI,
				truncate(course.Title, 200), truncate(course.Teacher, 128), truncate(course.Term, 64), now,
				truncate(course.CoverURL, 2048), course.CoverKnown)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return errServiceUnavailable
		}
		return nil
	})
}

// completedSyncCourses 复用当前请求或最近失败请求中已完整写入的课程，用于中断恢复。
// 只跳过失败请求之后写入的完整快照；更早的数据仍会在本次同步中刷新。
func (s *Service) completedSyncCourses(ctx context.Context, job courseSyncJob) (map[string]int64, error) {
	var requested time.Time
	if err := s.pool.QueryRow(ctx, `SELECT requested_at FROM public.learning_course_syncs WHERE id=$1`, job.ID).Scan(&requested); err != nil {
		return nil, errServiceUnavailable
	}
	threshold := requested
	var previous time.Time
	// 选择最近6小时内最早的一次失败请求，覆盖该次及后续失败请求中已完成的课程；
	// 不能只取最新失败（它可能尚未完成任何课程），否则恢复会从第一门重跑。
	err := s.pool.QueryRow(ctx, `SELECT requested_at FROM public.learning_course_syncs
		WHERE user_id=$1 AND id<>$2 AND status IN ('needs_attention','failed')
		  AND requested_at<$3 AND requested_at>=$4
		ORDER BY requested_at ASC LIMIT 1`, job.UserID, job.ID, requested, requested.Add(-6*time.Hour)).Scan(&previous)
	if err == nil && requested.Sub(previous) <= 6*time.Hour {
		threshold = previous
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, errServiceUnavailable
	}
	rows, err := s.pool.Query(ctx, `SELECT c.external_course_id,c.external_class_id,c.snapshot_version
		FROM public.learning_courses c
		WHERE c.user_id=$1 AND c.cycle_key='current' AND c.snapshot_version>0 AND c.synced_at>=$2`, job.UserID, threshold)
	if err != nil {
		return nil, errServiceUnavailable
	}
	defer rows.Close()
	completed := map[string]int64{}
	for rows.Next() {
		var courseID, classID string
		var version int64
		if err := rows.Scan(&courseID, &classID, &version); err != nil {
			return nil, errServiceUnavailable
		}
		completed[courseID+"/"+classID] = version
	}
	return completed, rows.Err()
}
