package learning

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// AccountIdentity 由认证服务在可信会话核对后传入；不接受用户名或客户端自述 UID。
// Subject 只在本调用栈内使用，写入前会转换为不可逆 HMAC 指纹。
type AccountIdentity struct {
	Provider    string
	Namespace   string
	Subject     string
	School      string
	DisplayName string
	VerifiedAt  time.Time
}

// identityFingerprint 校验并派生持久化用的身份指纹。
func (s *Service) identityFingerprint(namespace, subject string) (string, error) {
	if s.opts.IdentityFingerprint == nil {
		return "", apiErr(503, "identity_fingerprint_not_configured", "身份指纹未配置，暂不能绑定学习通账号")
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(subject) == "" {
		return "", validation("外部身份命名空间或主体为空")
	}
	fingerprint := strings.TrimSpace(s.opts.IdentityFingerprint(namespace, subject))
	if len(fingerprint) < 32 || len(fingerprint) > 128 || fingerprint == subject {
		return "", apiErr(503, "identity_fingerprint_invalid", "身份指纹配置无效")
	}
	for _, r := range fingerprint {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' ||
			r >= 'g' && r <= 'z' || r >= 'G' && r <= 'Z' || r == '-' || r == '_') {
			return "", apiErr(503, "identity_fingerprint_invalid", "身份指纹配置无效")
		}
	}
	return fingerprint, nil
}

// BindAccount 是服务间绑定入口，不注册为 HTTP 路由。
// 同一用户只能有一个有效学习通主体，同一主体也只能对应一个内部用户。
func (s *Service) BindAccount(ctx context.Context, userID string, identity AccountIdentity) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return s.BindAccountTx(ctx, tx, userID, identity)
	})
}

// BindAccountTx 在调用方事务内绑定身份，供认证首次登录与课程账号同时提交。
// userID 必须来自服务端刚创建/确认的内部用户，不能来自客户端请求体。
// 失败时由调用方事务整体回滚；本函数不自行提交。
func (s *Service) BindAccountTx(ctx context.Context, tx pgx.Tx, userID string, identity AccountIdentity) error {
	if tx == nil {
		return validation("身份绑定要求显式事务")
	}
	if userID == "" || !web.ValidID(userID) || identity.Provider != "chaoxing" {
		return validation("身份绑定参数无效")
	}
	if len(identity.Namespace) < 1 || len(identity.Namespace) > 128 || len(identity.Subject) < 1 || len(identity.Subject) > 512 {
		return validation("外部身份命名空间或主体为空")
	}
	fingerprint, err := s.identityFingerprint(identity.Namespace, identity.Subject)
	if err != nil {
		return err
	}
	if identity.VerifiedAt.IsZero() {
		identity.VerifiedAt = s.now()
	}
	var boundUser, boundFingerprint, boundNamespace, blockedCode string
	var blockedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT user_id::text, identity_fingerprint, identity_namespace, blocked_code, blocked_at
		FROM public.learning_accounts WHERE user_id = $1 FOR UPDATE`, userID).
		Scan(&boundUser, &boundFingerprint, &boundNamespace, &blockedCode, &blockedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errServiceUnavailable
	}
	if boundUser != "" && (boundFingerprint != fingerprint || boundNamespace != identity.Namespace) {
		return apiErr(409, "identity_conflict", "该用户已绑定其他学习通主体，需人工核验")
	}
	var otherUser string
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM public.learning_accounts
		WHERE provider = $1 AND identity_namespace = $2 AND identity_fingerprint = $3 FOR UPDATE`,
		identity.Provider, identity.Namespace, fingerprint).Scan(&otherUser)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errServiceUnavailable
	}
	if otherUser != "" && otherUser != userID {
		return apiErr(409, "identity_conflict", "该学习通主体已关联其他用户，需人工核验")
	}
	// 同主体的新鲜凭据验证可恢复会话失效；管理员限制、平台验证/拒绝和冷却独立保留。
	recoverIdentity := boundUser != "" && blockedCode == string(FailureIdentityInvalid) &&
		(blockedAt == nil || !identity.VerifiedAt.Before(*blockedAt))
	_, err = tx.Exec(ctx, `INSERT INTO public.learning_accounts
		(user_id, provider, identity_namespace, identity_fingerprint, school, display_name, verified_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (user_id) DO UPDATE SET
			school = EXCLUDED.school, display_name = EXCLUDED.display_name,
			verified_at = EXCLUDED.verified_at, version = public.learning_accounts.version + 1,
			blocked_code = CASE WHEN $8 THEN '' ELSE public.learning_accounts.blocked_code END,
			blocked_reason = CASE WHEN $8 THEN '' ELSE public.learning_accounts.blocked_reason END,
			blocked_at = CASE WHEN $8 THEN NULL ELSE public.learning_accounts.blocked_at END,
			updated_at = now()`,
		userID, identity.Provider, identity.Namespace, fingerprint, truncate(identity.School, 128),
		truncate(identity.DisplayName, 128), identity.VerifiedAt, recoverIdentity)
	if err != nil {
		return errServiceUnavailable
	}
	return nil
}

// AccountBlocked 返回账号阻断摘要，用于用户/管理员界面；不返回会话。
type AccountBlocked struct {
	Code      string
	Reason    string
	BlockedAt *time.Time
	Until     *time.Time
}

func (s *Service) accountStatus(ctx context.Context, userID string) (AccountBlocked, error) {
	var item AccountBlocked
	err := s.pool.QueryRow(ctx, `SELECT blocked_code, blocked_reason, blocked_at, blocked_until
		FROM public.learning_accounts WHERE user_id = $1`, userID).
		Scan(&item.Code, &item.Reason, &item.BlockedAt, &item.Until)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, nil
	}
	if err != nil {
		return item, errServiceUnavailable
	}
	return item, nil
}

// CourseSync 是一次异步课程同步操作。
type CourseSync struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	AttemptCount    int        `json:"attempt_count"`
	ErrorCode       string     `json:"error_code,omitempty"`
	SnapshotVersion int64      `json:"snapshot_version"`
	CourseCount     int        `json:"course_count"`
	RequestedAt     time.Time  `json:"requested_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

// RequestCourseSync 去重创建异步同步；同一用户同时只有一个排队或运行中的同步。
// 网络读取在事务外执行，不持有数据库写锁。
func (s *Service) RequestCourseSync(ctx context.Context, actor Actor) (CourseSync, bool, error) {
	now := s.now()
	created := false
	var result CourseSync
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		account, err := s.loadAccount(ctx, tx, actor.ID)
		if err != nil {
			return err
		}
		if code := s.accountBlocked(account, now); code != "" {
			return apiErr(409, "account_blocked", "账号当前不可发起课程同步").withScope("account")
		}
		var existingID string
		err = tx.QueryRow(ctx, `SELECT id::text FROM public.learning_course_syncs
			WHERE user_id = $1 AND dedupe_key = 'all' AND status IN ('queued', 'running')
			FOR UPDATE`, actor.ID).Scan(&existingID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return errServiceUnavailable
		}
		if existingID == "" {
			id := newID()
			_, err = tx.Exec(ctx, `INSERT INTO public.learning_course_syncs
				(id, user_id, dedupe_key, status, requested_by_kind, requested_by_id, requested_at, updated_at)
				VALUES ($1, $2, 'all', 'queued', $3, $4, $5, $5)`,
				id, actor.ID, actor.Kind, actor.ID, now)
			if err != nil {
				return errServiceUnavailable
			}
			existingID = id
			created = true
			if err := writeOutbox(ctx, tx, outboxEvent{
				Topic: "course.sync_requested", ResourceType: "course_sync", ResourceID: id,
				UserID: actor.ID, Payload: map[string]any{"status": "queued"},
			}); err != nil {
				return err
			}
		}
		item, err := readCourseSync(ctx, tx, existingID)
		if err != nil {
			return err
		}
		result = item
		return nil
	})
	if err != nil {
		return CourseSync{}, false, err
	}
	return result, created, nil
}

func (s *Service) LatestCourseSync(ctx context.Context, actor Actor) (CourseSync, error) {
	var item CourseSync
	err := s.pool.QueryRow(ctx, `SELECT id::text, status, attempt_count, error_code, snapshot_version,
		course_count, requested_at, started_at, finished_at
		FROM public.learning_course_syncs WHERE user_id = $1
		ORDER BY requested_at DESC LIMIT 1`, actor.ID).Scan(
		&item.ID, &item.Status, &item.AttemptCount, &item.ErrorCode, &item.SnapshotVersion,
		&item.CourseCount, &item.RequestedAt, &item.StartedAt, &item.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, errNotFound
	}
	if err != nil {
		return item, errServiceUnavailable
	}
	return item, nil
}

func (s *Service) GetCourseSync(ctx context.Context, actor Actor, id string) (CourseSync, error) {
	var item CourseSync
	err := s.pool.QueryRow(ctx, `SELECT id::text, status, attempt_count, error_code, snapshot_version,
		course_count, requested_at, started_at, finished_at
		FROM public.learning_course_syncs WHERE id = $1 AND user_id = $2`, id, actor.ID).Scan(
		&item.ID, &item.Status, &item.AttemptCount, &item.ErrorCode, &item.SnapshotVersion,
		&item.CourseCount, &item.RequestedAt, &item.StartedAt, &item.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, errNotFound
	}
	if err != nil {
		return item, errServiceUnavailable
	}
	return item, nil
}

func readCourseSync(ctx context.Context, querier rowQuerier, id string) (CourseSync, error) {
	var item CourseSync
	err := querier.QueryRow(ctx, `SELECT id::text, status, attempt_count, error_code, snapshot_version,
		course_count, requested_at, started_at, finished_at
		FROM public.learning_course_syncs WHERE id = $1`, id).Scan(
		&item.ID, &item.Status, &item.AttemptCount, &item.ErrorCode, &item.SnapshotVersion,
		&item.CourseCount, &item.RequestedAt, &item.StartedAt, &item.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, errNotFound
	}
	if err != nil {
		return item, errServiceUnavailable
	}
	return item, nil
}

// CourseSummary 是课程列表行。
type CourseSummary struct {
	ID                       string     `json:"id"`
	Title                    string     `json:"title"`
	Teacher                  string     `json:"teacher"`
	Term                     string     `json:"term"`
	ExternalCourseID         string     `json:"external_course_id"`
	ExternalClassID          string     `json:"external_class_id"`
	SnapshotVersion          int64      `json:"snapshot_version"`
	SyncedAt                 *time.Time `json:"synced_at,omitempty"`
	Stale                    bool       `json:"stale"`
	ChapterCount             int        `json:"chapter_count"`
	TaskPointCount           int        `json:"task_point_count"`
	IncompleteTaskPointCount int        `json:"incomplete_task_point_count"`
	ResourceCount            int        `json:"resource_count"`
}

type CourseFilter struct {
	Limit  int
	Cursor string
	Search string
}

// ListCourses 只返回 Actor 自己的课程。
func (s *Service) ListCourses(ctx context.Context, actor Actor, filter CourseFilter) (Page[CourseSummary], error) {
	limit := clampLimit(filter.Limit)
	position, err := decodeCursor(filter.Cursor)
	if err != nil {
		return Page[CourseSummary]{}, err
	}
	search := truncate(filter.Search, 80)
	rows, err := s.pool.Query(ctx, `SELECT c.id::text, c.title, c.teacher, c.term, c.external_course_id,
		c.external_class_id, c.snapshot_version, c.synced_at, c.updated_at,
		COALESCE(s.chapter_count, 0), COALESCE(s.task_point_count, 0), COALESCE(s.incomplete_task_point_count, 0),
		COALESCE(s.attachment_count - s.task_point_count, 0)
		FROM public.learning_courses c
		LEFT JOIN public.learning_course_snapshots s ON s.course_id = c.id AND s.user_id = c.user_id AND s.version = c.snapshot_version
		WHERE c.user_id = $1
		  AND ($2 = '' OR c.title ILIKE '%' || $2 || '%' OR c.external_course_id = $2)
		  AND ($3::timestamptz IS NULL OR (c.updated_at, c.id) < ($3::timestamptz, $4::uuid))
		ORDER BY c.updated_at DESC, c.id DESC
		LIMIT $5`, actor.ID, search, nullableTime(position.Time), nullableID(position.ID), limit+1)
	if err != nil {
		return Page[CourseSummary]{}, errServiceUnavailable
	}
	defer rows.Close()
	items := make([]CourseSummary, 0, limit+1)
	times := make([]time.Time, 0, limit+1)
	now := s.now()
	for rows.Next() {
		var item CourseSummary
		var updated time.Time
		if err := rows.Scan(&item.ID, &item.Title, &item.Teacher, &item.Term, &item.ExternalCourseID,
			&item.ExternalClassID, &item.SnapshotVersion, &item.SyncedAt, &updated,
			&item.ChapterCount, &item.TaskPointCount, &item.IncompleteTaskPointCount,
			&item.ResourceCount); err != nil {
			return Page[CourseSummary]{}, errServiceUnavailable
		}
		item.Stale = item.SyncedAt == nil || now.Sub(*item.SyncedAt) > s.opts.CourseSnapshotTTL
		items = append(items, item)
		times = append(times, updated)
	}
	if err := rows.Err(); err != nil {
		return Page[CourseSummary]{}, errServiceUnavailable
	}
	page := Page[CourseSummary]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeCursor(times[limit-1], last.ID)
	}
	return page, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

type AttachmentView struct {
	ID                string     `json:"id"`
	ObjectID          string     `json:"object_id"`
	TaskPointID       string     `json:"task_point_id,omitempty"`
	Kind              string     `json:"kind"`
	Title             string     `json:"title"`
	DurationSeconds   float64    `json:"duration_seconds"`
	PlayedSeconds     float64    `json:"played_seconds"`
	PlatformPassed    bool       `json:"platform_passed"`
	PassedKnown       bool       `json:"passed_known"`
	IsTaskPoint       bool       `json:"is_task_point"`
	CountedIncomplete bool       `json:"counted_incomplete"`
	ProgressCheckedAt *time.Time `json:"progress_checked_at,omitempty"`
}

type ChapterView struct {
	ID          string           `json:"id"`
	ExternalID  string           `json:"external_id"`
	Title       string           `json:"title"`
	SortOrder   int              `json:"sort_order"`
	Attachments []AttachmentView `json:"attachments"`
}

type CourseDetail struct {
	CourseSummary
	Chapters []ChapterView `json:"chapters"`
}

// GetCourse 读取当前快照；旧快照保留但不出现在正常界面。
func (s *Service) GetCourse(ctx context.Context, actor Actor, courseID string) (CourseDetail, error) {
	var detail CourseDetail
	if !web.ValidID(courseID) {
		return detail, errNotFound
	}
	err := s.pool.QueryRow(ctx, `SELECT c.id::text, c.title, c.teacher, c.term, c.external_course_id,
		c.external_class_id, c.snapshot_version, c.synced_at,
		COALESCE(s.chapter_count, 0), COALESCE(s.task_point_count, 0), COALESCE(s.incomplete_task_point_count, 0),
		COALESCE((SELECT count(*) FROM public.learning_attachments a
			WHERE a.course_id = c.id AND a.user_id = c.user_id AND a.snapshot_version = c.snapshot_version
			  AND NOT a.is_task_point), 0)
		FROM public.learning_courses c
		LEFT JOIN public.learning_course_snapshots s ON s.course_id = c.id AND s.user_id = c.user_id AND s.version = c.snapshot_version
		WHERE c.id = $1 AND c.user_id = $2`, courseID, actor.ID).Scan(
		&detail.ID, &detail.Title, &detail.Teacher, &detail.Term, &detail.ExternalCourseID,
		&detail.ExternalClassID, &detail.SnapshotVersion, &detail.SyncedAt,
		&detail.ChapterCount, &detail.TaskPointCount, &detail.IncompleteTaskPointCount, &detail.ResourceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return detail, errNotFound
	}
	if err != nil {
		return detail, errServiceUnavailable
	}
	now := s.now()
	detail.Stale = detail.SyncedAt == nil || now.Sub(*detail.SyncedAt) > s.opts.CourseSnapshotTTL
	rows, err := s.pool.Query(ctx, `SELECT id::text, external_id, title, sort_order
		FROM public.learning_chapters
		WHERE course_id = $1 AND user_id = $2 AND snapshot_version = $3
		ORDER BY sort_order, external_id`, courseID, actor.ID, detail.SnapshotVersion)
	if err != nil {
		return detail, errServiceUnavailable
	}
	defer rows.Close()
	chapters := make([]ChapterView, 0)
	index := map[string]int{}
	for rows.Next() {
		var item ChapterView
		if err := rows.Scan(&item.ID, &item.ExternalID, &item.Title, &item.SortOrder); err != nil {
			return detail, errServiceUnavailable
		}
		item.Attachments = []AttachmentView{}
		index[item.ID] = len(chapters)
		chapters = append(chapters, item)
	}
	if err := rows.Err(); err != nil {
		return detail, errServiceUnavailable
	}
	attachmentRows, err := s.pool.Query(ctx, `SELECT chapter_id::text, id::text, external_object_id,
		task_point_id, kind, title, duration_seconds, played_seconds, platform_passed, passed_known,
		is_task_point, counted_incomplete, progress_checked_at
		FROM public.learning_attachments
		WHERE course_id = $1 AND user_id = $2 AND snapshot_version = $3
		ORDER BY chapter_id, sort_order, external_object_id`, courseID, actor.ID, detail.SnapshotVersion)
	if err != nil {
		return detail, errServiceUnavailable
	}
	defer attachmentRows.Close()
	for attachmentRows.Next() {
		var item AttachmentView
		var chapterID string
		if err := attachmentRows.Scan(&chapterID, &item.ID, &item.ObjectID, &item.TaskPointID, &item.Kind,
			&item.Title, &item.DurationSeconds, &item.PlayedSeconds, &item.PlatformPassed, &item.PassedKnown,
			&item.IsTaskPoint, &item.CountedIncomplete, &item.ProgressCheckedAt); err != nil {
			return detail, errServiceUnavailable
		}
		if position, ok := index[chapterID]; ok {
			chapters[position].Attachments = append(chapters[position].Attachments, item)
		}
	}
	if err := attachmentRows.Err(); err != nil {
		return detail, errServiceUnavailable
	}
	detail.Chapters = chapters
	return detail, nil
}
