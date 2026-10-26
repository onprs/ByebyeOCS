package learning

import (
	"context"

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
				 external_class_id,external_cpi,cycle_key,title,teacher,term,updated_at)
				VALUES ($1,$2,'chaoxing',$3,$4,$5,$6,$7,'current',COALESCE(NULLIF($8,''),$5),$9,$10,$11)
				ON CONFLICT (user_id,provider,external_course_id,external_class_id,cycle_key)
				DO UPDATE SET title=COALESCE(NULLIF($8,''),learning_courses.title),
				 teacher=COALESCE(NULLIF($9,''),learning_courses.teacher),
				 term=COALESCE(NULLIF($10,''),learning_courses.term), external_cpi=EXCLUDED.external_cpi,
				 updated_at=EXCLUDED.updated_at, version=learning_courses.version+1`,
				newID(), account.UserID, account.Namespace, account.Fingerprint,
				course.ExternalCourseID, course.ExternalClassID, course.CPI,
				truncate(course.Title, 200), truncate(course.Teacher, 128), truncate(course.Term, 64), now)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return errServiceUnavailable
		}
		return nil
	})
}

// completedSyncCourses 只复用本次同步请求已经完整写入的课程，用于中断恢复。
func (s *Service) completedSyncCourses(ctx context.Context, job courseSyncJob) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.external_course_id,c.external_class_id,c.snapshot_version
		FROM public.learning_courses c JOIN public.learning_course_syncs sync ON sync.user_id=c.user_id
		WHERE sync.id=$1 AND c.cycle_key='current' AND c.synced_at>=sync.requested_at AND c.snapshot_version>0`, job.ID)
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
