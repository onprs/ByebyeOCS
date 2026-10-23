//go:build integration

package learning

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLongChineseCourseMetadataRemainsValidUTF8(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	snapshot := platform.snapshots["101/202"]
	snapshot.Title = strings.Repeat("课程", 150)
	snapshot.Chapters[0].Title = strings.Repeat("章节", 150)
	snapshot.Chapters[0].Attachments[0].Title = strings.Repeat("学习任务", 90)
	platform.snapshots["101/202"] = snapshot
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	course, err := service.GetCourse(context.Background(), Actor{ID: user, Kind: "user"}, courseIDForUser(t, pool, user))
	if err != nil {
		t.Fatal(err)
	}
	if len(course.Chapters) == 0 || len(course.Chapters[0].Attachments) == 0 {
		t.Fatal("长中文导致课程同步失败")
	}
	for _, value := range []string{course.Title, course.Chapters[0].Title, course.Chapters[0].Attachments[0].Title} {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 200 {
			t.Fatalf("中文截断不合法: %q", value)
		}
	}
}
