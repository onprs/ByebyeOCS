package chaoxing

import (
	"context"
	"errors"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"strings"
	"testing"
)

func TestWorkClosedBannerOverridesRemainingAnswerControls(t *testing.T) {
	for _, test := range []struct {
		name, notice string
		closed       bool
	}{
		{"截止保留表单", "<span>（已截止，不能作答）</span>", true},
		{"关闭状态", "<p>该测验已关闭</p>", true},
		{"直接状态块", "<div>测验已截止</div>", true},
		{"隐藏状态祖先", "<div><div hidden><span>已截止，不能作答</span></div></div>", false},
		{"隐藏模板", "<div hidden><span>已截止，不能作答</span></div>", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newWorkFixture(t)
			f.pageHTML = strings.Replace(f.pageHTML, "</body>", test.notice+"</body>", 1)
			_, err := f.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
			var failure *learning.Failure
			if test.closed {
				if !errors.As(err, &failure) || failure.Class != learning.FailurePlatformClosed {
					t.Fatalf("关闭状态未阻止取答案：%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	f := newWorkFixture(t)
	f.pageHTML = strings.ReplaceAll(f.pageHTML, "题干", "解释已截止不能作答的案例")
	if _, err := f.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef()); err != nil {
		t.Fatalf("题目文字误判关闭：%v", err)
	}
}
