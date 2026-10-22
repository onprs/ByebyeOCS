package chaoxing

import (
	"context"
	"strings"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

func TestWorkQuestionTextDoesNotActAsPlatformChallenge(t *testing.T) {
	fixture := newWorkFixture(t)
	fixture.pageHTML = strings.ReplaceAll(fixture.pageHTML, "题干", "解释验证码、登录失效与访问权限的关系")
	fixture.pageHTML = strings.Replace(fixture.pageHTML, "</body>", `<p>请说明验证码的用途。</p><div style="display: none"><input name="captcha" type="text"></div></body>`, 1)
	sheet, err := fixture.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
	if err != nil || len(sheet.Questions) != 5 || sheet.Submitted {
		t.Fatal("普通题目文字或隐藏控件被误判为平台验证", err)
	}
}

func TestWorkVisibleChallengeAndErrorControlsStop(t *testing.T) {
	for _, scenario := range []struct {
		name, control string
		class         learning.FailureClass
	}{
		{"验证输入", `<input name="verifyCode" type="text">`, learning.FailureVerificationNeeded},
		{"验证码组件", `<div id="captchaContainer"></div>`, learning.FailureVerificationNeeded},
		{"权限通知", `<div role="alert">没有权限访问该作业</div>`, learning.FailureForbidden},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newWorkFixture(t)
			fixture.pageHTML = strings.Replace(fixture.pageHTML, "</body>", scenario.control+"</body>", 1)
			_, err := fixture.platform(t).ReadWork(context.Background(), encodeTestSession(t), workTaskRef())
			requireWorkFailure(t, err, scenario.class)
			if fixture.postCount != 0 {
				t.Fatal("平台验证期间仍提交答案")
			}
		})
	}
}

func TestWorkSubmissionRiskFieldsTakePriorityOverSuccessFlag(t *testing.T) {
	for _, scenario := range []struct {
		body  string
		class learning.FailureClass
	}{
		{`{"status":true,"needCaptcha":true}`, learning.FailureVerificationNeeded},
		{`{"status":true,"code":403}`, learning.FailureForbidden},
	} {
		t.Run(string(scenario.class), func(t *testing.T) {
			fixture := newWorkFixture(t)
			fixture.submitBody = scenario.body
			platform := fixture.platform(t)
			session := encodeTestSession(t)
			sheet, err := platform.ReadWork(context.Background(), session, workTaskRef())
			if err != nil {
				t.Fatal(err)
			}
			_, err = platform.SubmitWork(context.Background(), session, workTaskRef(), sheet.Fingerprint, workAnswers(), nil)
			requireWorkFailure(t, err, scenario.class)
			if fixture.postCount != 1 {
				t.Fatal("风险响应触发重复 POST", fixture.postCount)
			}
		})
	}
}
