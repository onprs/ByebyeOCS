package learning

import "testing"

func TestDedupeSnapshotAttachmentsKeepsTaskPointAndProgress(t *testing.T) {
	items := []PlatformAttachment{
		{ObjectID: "video-1", Kind: "video", Title: "视频", CardIndex: 1},
		{ObjectID: "video-1", Kind: "video", TaskPointID: "tp-1", Title: "视频任务点", PlayedSeconds: 42, PassedKnown: true, Passed: true, CardIndex: 2},
		{ObjectID: "video-2", Kind: "video", TaskPointID: "tp-2", CardIndex: 3},
	}
	unique := dedupeSnapshotAttachments(items)
	if len(unique) != 2 {
		t.Fatalf("重复附件未合并：%+v", unique)
	}
	if unique[0].TaskPointID != "tp-1" || !unique[0].PassedKnown || !unique[0].Passed || unique[0].PlayedSeconds != 42 || unique[0].CardIndex != 2 {
		t.Fatalf("合并后丢失任务点或进度：%+v", unique[0])
	}
	if unique[1].ObjectID != "video-2" {
		t.Fatal("不同对象被错误合并")
	}
}
