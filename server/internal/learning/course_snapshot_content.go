package learning

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type courseSnapshotCounts struct {
	Chapters, Attachments, TaskPoints, Incomplete int
}

// validateCourseChapters 在写库前检查整个目录，禁止孤立节点、环和带任务的目录分组。
// 外部标识参与稳定任务关联，不能截断后再保存父子关系。
func validateCourseChapters(chapters []PlatformChapter) error {
	if len(chapters) > 1000 {
		return Fail(FailureInvalidTask, "课程快照章节数量超出支持范围")
	}
	byID := make(map[string]PlatformChapter, len(chapters))
	for _, chapter := range chapters {
		if strings.TrimSpace(chapter.ExternalID) == "" || len(chapter.ExternalID) > 128 || len(chapter.ParentExternalID) > 128 || len(chapter.Number) > 128 {
			return Fail(FailureInvalidTask, "课程快照章节标识或编号无效")
		}
		if _, exists := byID[chapter.ExternalID]; exists {
			return Fail(FailureInvalidTask, "课程快照章节标识重复")
		}
		if chapter.IsGroup && len(chapter.Attachments) != 0 {
			return Fail(FailureInvalidTask, "课程目录分组不能包含任务附件")
		}
		seenTasks := make(map[string]PlatformAttachment, len(chapter.Attachments))
		for _, attachment := range chapter.Attachments {
			if attachment.TaskPointID == "" {
				continue
			}
			previous, exists := seenTasks[attachment.TaskPointID]
			if exists && (previous.ObjectID != attachment.ObjectID || previous.Kind != attachment.Kind ||
				previous.PassedKnown && attachment.PassedKnown && previous.Passed != attachment.Passed) {
				return Fail(FailureInvalidTask, "课程快照任务点标识或进度相互冲突")
			}
			if !exists || !previous.PassedKnown || attachment.PassedKnown {
				seenTasks[attachment.TaskPointID] = attachment
			}
		}
		byID[chapter.ExternalID] = chapter
	}
	depths, visiting := make(map[string]int, len(chapters)), make(map[string]bool, len(chapters))
	var depth func(string) (int, error)
	depth = func(id string) (int, error) {
		if id == "" {
			return 0, nil
		}
		if value, known := depths[id]; known {
			return value, nil
		}
		chapter, exists := byID[id]
		if !exists || visiting[id] {
			return 0, Fail(FailureInvalidTask, "课程快照章节父子关系缺失或成环")
		}
		visiting[id] = true
		parentDepth, err := depth(chapter.ParentExternalID)
		if err != nil {
			return 0, err
		}
		if parentDepth >= 32 {
			return 0, Fail(FailureInvalidTask, "课程快照章节层级超出支持范围")
		}
		visiting[id] = false
		depths[id] = parentDepth + 1
		return parentDepth + 1, nil
	}
	for id := range byID {
		if _, err := depth(id); err != nil {
			return err
		}
	}
	return nil
}

// writeCourseSnapshotContent 先读取稳定章节 UUID，再按依赖顺序批量写入章节、附件和任务。
// 所有写入仍属于外层快照事务，任意一条失败都会整体回滚。
func writeCourseSnapshotContent(ctx context.Context, tx pgx.Tx, account accountRecord, courseID, cycle string, version int64, snapshot PlatformCourseSnapshot, now time.Time) (courseSnapshotCounts, error) {
	counts := courseSnapshotCounts{Chapters: len(snapshot.Chapters)}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (external_id) external_id, id::text
		FROM public.learning_chapters WHERE course_id=$1 AND user_id=$2
		ORDER BY external_id, snapshot_version DESC`, courseID, account.UserID)
	if err != nil {
		return counts, errServiceUnavailable
	}
	chapterIDs := make(map[string]string, len(snapshot.Chapters))
	for rows.Next() {
		var externalID, id string
		if err := rows.Scan(&externalID, &id); err != nil {
			rows.Close()
			return counts, errServiceUnavailable
		}
		chapterIDs[externalID] = id
	}
	rows.Close()
	if rows.Err() != nil {
		return counts, errServiceUnavailable
	}
	batch := &pgx.Batch{}
	flush := func() error {
		if batch.Len() == 0 {
			return nil
		}
		err := tx.SendBatch(ctx, batch).Close()
		batch = &pgx.Batch{}
		if err != nil {
			return errServiceUnavailable
		}
		return nil
	}
	for position, chapter := range snapshot.Chapters {
		chapterID := chapterIDs[chapter.ExternalID]
		if chapterID == "" {
			chapterID = newID()
		}
		batch.Queue(`INSERT INTO public.learning_chapters
			(id,course_id,user_id,snapshot_version,external_id,parent_external_id,title,sort_order,chapter_number,is_group)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO UPDATE SET snapshot_version=EXCLUDED.snapshot_version,
				parent_external_id=EXCLUDED.parent_external_id,title=EXCLUDED.title,
				sort_order=EXCLUDED.sort_order,chapter_number=EXCLUDED.chapter_number,is_group=EXCLUDED.is_group`,
			chapterID, courseID, account.UserID, version, chapter.ExternalID, chapter.ParentExternalID,
			truncate(chapter.Title, 200), position, chapter.Number, chapter.IsGroup)
		for index, attachment := range dedupeSnapshotAttachments(chapter.Attachments) {
			// 有界批次避免大课程在内存中积攒全部 SQL 和参数。
			if batch.Len() >= 256 {
				if err := flush(); err != nil {
					return counts, err
				}
			}
			counts.Attachments++
			taskPoint := attachment.TaskPointID != ""
			if taskPoint {
				counts.TaskPoints++
				if !attachment.Passed {
					counts.Incomplete++
				}
			}
			if attachment.CardIndex < 0 || attachment.CardIndex > 31 {
				return counts, Fail(FailureInvalidTask, "课程快照附件卡片页码无效")
			}
			attachmentID, played := newID(), nonNegative(attachment.PlayedSeconds)
			batch.Queue(`INSERT INTO public.learning_attachments
				(id,course_id,chapter_id,user_id,snapshot_version,external_object_id,task_point_id,
				 kind,title,duration_seconds,played_seconds,platform_passed,passed_known,
				 is_task_point,counted_incomplete,sort_order,card_index,progress_checked_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
				attachmentID, courseID, chapterID, account.UserID, version, attachment.ObjectID, attachment.TaskPointID,
				truncate(attachment.Kind, 32), truncate(attachment.Title, 200), nonNegative(attachment.DurationSeconds),
				played, attachment.Passed, attachment.PassedKnown, taskPoint, taskPoint && !attachment.Passed,
				index, attachment.CardIndex, checkedAt(attachment.PassedKnown, now))
			if !taskPoint {
				continue
			}
			check := map[string]any{"status": CheckUnconfirmed, "passed": false, "source": "course_snapshot",
				"checked_at": now, "play_time_seconds": played}
			state, platformStatus, confirmedAt := StatePending, CheckUnconfirmed, any(nil)
			if attachment.PassedKnown && attachment.Passed {
				check["status"], check["passed"] = CheckConfirmed, true
				state, platformStatus, confirmedAt = StateConfirmed, CheckConfirmed, now
			}
			checkJSON, _ := json.Marshal(check)
			batch.Queue(`INSERT INTO public.learning_tasks
				(id,user_id,course_id,chapter_id,attachment_id,provider,identity_namespace,
				 identity_fingerprint,cycle_key,course_external_id,class_external_id,chapter_external_id,
				 task_point_id,task_kind,title,state,platform_status,platform_checked_at,confirmed_at,platform_check,updated_at)
				VALUES ($1,$2,$3,$4,$5,'chaoxing',$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$17)
				ON CONFLICT (provider,identity_namespace,identity_fingerprint,course_external_id,
				             class_external_id,chapter_external_id,task_point_id,cycle_key)
				DO UPDATE SET course_id=EXCLUDED.course_id,chapter_id=EXCLUDED.chapter_id,
					attachment_id=EXCLUDED.attachment_id,title=EXCLUDED.title,
					task_kind=EXCLUDED.task_kind,updated_at=EXCLUDED.updated_at`,
				newID(), account.UserID, courseID, chapterID, attachmentID, account.Namespace, account.Fingerprint,
				cycle, snapshot.Course.ExternalCourseID, snapshot.Course.ExternalClassID, chapter.ExternalID,
				attachment.TaskPointID, truncate(attachment.Kind, 32), firstNonEmpty(attachment.Title, attachment.ObjectID),
				state, platformStatus, now, confirmedAt, checkJSON)
		}
		if batch.Len() >= 256 {
			if err := flush(); err != nil {
				return counts, err
			}
		}
	}
	if err := flush(); err != nil {
		return counts, err
	}
	return counts, nil
}
