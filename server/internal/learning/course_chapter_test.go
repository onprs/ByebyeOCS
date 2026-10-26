package learning

import (
	"strconv"
	"testing"
)

func TestCourseChapterValidationRequiresCompleteAcyclicTree(t *testing.T) {
	valid := []PlatformChapter{
		{ExternalID: "group-intro", Number: "1", IsGroup: true},
		{ExternalID: "900", ParentExternalID: "group-intro", Number: "1.1"},
		{ExternalID: "100", ParentExternalID: "900", Number: "1.1.1"},
	}
	if err := validateCourseChapters(valid); err != nil {
		t.Fatal(err)
	}
	for name, chapters := range map[string][]PlatformChapter{
		"缺少父节点":       {{ExternalID: "900", ParentExternalID: "missing"}},
		"父子成环":        {{ExternalID: "900", ParentExternalID: "100"}, {ExternalID: "100", ParentExternalID: "900"}},
		"重复标识":        {{ExternalID: "900"}, {ExternalID: "900"}},
		"分组携带任务":      {{ExternalID: "group", IsGroup: true, Attachments: []PlatformAttachment{{ObjectID: "video", TaskPointID: "task"}}}},
		"同一任务点关联不同对象": {{ExternalID: "900", Attachments: []PlatformAttachment{{ObjectID: "video-1", TaskPointID: "task"}, {ObjectID: "video-2", TaskPointID: "task"}}}},
		"同一任务点状态冲突":   {{ExternalID: "900", Attachments: []PlatformAttachment{{ObjectID: "video", TaskPointID: "task", PassedKnown: true, Passed: true}, {ObjectID: "video", TaskPointID: "task", PassedKnown: true, Passed: false}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCourseChapters(chapters); err == nil {
				t.Fatal("不完整或冲突目录没有被拒绝")
			}
		})
	}
	deep := make([]PlatformChapter, 33)
	for index := range deep {
		deep[index].ExternalID = strconv.Itoa(index + 1)
		if index > 0 {
			deep[index].ParentExternalID = deep[index-1].ExternalID
		}
	}
	if err := validateCourseChapters(deep); err == nil {
		t.Fatal("超出支持深度的目录没有被拒绝")
	}
}

func TestDedupeSnapshotKeepsSeparateTaskPointProgress(t *testing.T) {
	items := []PlatformAttachment{
		{ObjectID: "video", TaskPointID: "first", Kind: "video", CardIndex: 1, PassedKnown: true, Passed: true, PlayedSeconds: 90},
		{ObjectID: "video", TaskPointID: "second", Kind: "video", CardIndex: 2, PassedKnown: true, Passed: false, PlayedSeconds: 5},
		{ObjectID: "video", TaskPointID: "second", Kind: "video", CardIndex: 2, PassedKnown: true, PlayedSeconds: 5},
	}
	unique := dedupeSnapshotAttachments(items)
	if len(unique) != 2 || unique[0].TaskPointID != "first" || unique[1].TaskPointID != "second" || unique[1].Passed || unique[1].PlayedSeconds != 5 || unique[1].CardIndex != 2 {
		t.Fatalf("同一媒体的任务点或进度被合并：%+v", unique)
	}
}

func TestOrdinaryResourceProgressCannotConfirmTaskPoint(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		resource := PlatformAttachment{ObjectID: "video", Kind: "video", Title: "普通资料", CardIndex: 0, PassedKnown: true, Passed: true, PlayedSeconds: 90}
		task := PlatformAttachment{ObjectID: "video", TaskPointID: "task", Kind: "video", Title: "视频任务", CardIndex: 2, PassedKnown: true, Passed: false, PlayedSeconds: 5}
		items := []PlatformAttachment{resource, task}
		if reversed {
			items[0], items[1] = items[1], items[0]
		}
		unique := dedupeSnapshotAttachments(items)
		if len(unique) != 1 || unique[0].TaskPointID != "task" || unique[0].Passed || !unique[0].PassedKnown || unique[0].PlayedSeconds != 5 || unique[0].CardIndex != 2 || unique[0].Title != "视频任务" {
			t.Fatalf("普通资料状态污染了任务点：%+v", unique)
		}
	}
}
