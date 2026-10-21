package learning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 20, 6, 30, 0, 123456789, time.UTC)
	id := "11111111-2222-4333-8444-555555555555"
	cursor := encodeCursor(at, id)
	decoded, err := decodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Time.Equal(at) || decoded.ID != id {
		t.Fatal("游标往返不一致")
	}
	if _, err := decodeCursor("not-a-cursor"); err == nil {
		t.Fatal("无效游标被接受")
	}
	if _, err := decodeCursor(encodeCursor(at, "short")); err == nil {
		t.Fatal("无效编号游标被接受")
	}
}

func TestNormalizePlanInput(t *testing.T) {
	course := "11111111-2222-4333-8444-555555555555"
	chapter := "66666666-7777-4888-8999-000000000000"
	valid := PlanInput{Name: " 晚间安排 ", CourseIDs: []string{course}, TaskTypes: []string{"video"},
		Scope: PlanScopeInput{Kind: "all"}, Priority: 20}
	normalized, err := normalizePlanInput(valid)
	if err != nil || normalized.Name != "晚间安排" || normalized.Scope.Kind != "all" {
		t.Fatalf("有效安排被拒绝: %v", err)
	}
	cases := []PlanInput{
		{Name: "", CourseIDs: []string{course}, TaskTypes: []string{"video"}},
		{Name: "x", CourseIDs: []string{course, course}, TaskTypes: []string{"video"}},
		{Name: "x", CourseIDs: []string{course}, TaskTypes: []string{"unknown"}},
		{Name: "x", CourseIDs: []string{course}, TaskTypes: []string{"video"}, Priority: 101},
		{Name: "x", CourseIDs: []string{course}, TaskTypes: []string{"video"},
			Scope: PlanScopeInput{Kind: "selected"}},
		{Name: "x", CourseIDs: []string{course}, TaskTypes: []string{"video"},
			Scope: PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: course}}}},
	}
	for index, input := range cases {
		if _, err := normalizePlanInput(input); err == nil {
			t.Fatalf("无效安排 %d 被接受", index)
		}
	}
	selected := valid
	selected.Scope = PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{
		{CourseID: course, ChapterID: chapter}, {CourseID: course, ChapterID: chapter}}}
	normalized, err = normalizePlanInput(selected)
	if err != nil || len(normalized.Scope.Chapters) != 1 {
		t.Fatalf("重复章节未去重: %v %d", err, len(normalized.Scope.Chapters))
	}
}

func TestFailureClassificationDefaults(t *testing.T) {
	failure := failureOf(errors.New("opaque upstream detail"))
	if failure.Class != FailureTransient || failure.Detail != "执行未完成" {
		t.Fatal("未知错误未按有界临时失败处理")
	}
	typed := &Failure{Class: FailureRateLimited, RetryAfter: time.Minute, Detail: "限流"}
	wrapped := errors.New("wrapper")
	if failureOf(typed).Class != FailureRateLimited {
		t.Fatal("分类失败未保留")
	}
	_ = wrapped
	service := &Service{opts: Options{RetryBase: time.Second}}
	first := service.retryDelay(1, nil)
	second := service.retryDelay(2, nil)
	if !(second > first) || first < time.Second {
		t.Fatalf("退避未增长或过小: %v %v", first, second)
	}
	if service.retryDelay(100, &Failure{RetryAfter: time.Hour}) != 15*time.Minute {
		t.Fatal("退避上限未生效")
	}
}

func TestEvidenceJSONDefaults(t *testing.T) {
	observation, media := evidenceJSON(ExecutionResult{})
	if string(observation) != "{}" {
		t.Fatal("空观察不应制造字段")
	}
	if !strings.Contains(string(media), `"available":false`) {
		t.Fatal("缺少媒体证据时必须明确不可用")
	}
	negative := -3.0
	observation, media = evidenceJSON(ExecutionResult{Observation: Observation{
		Fraction: &negative, Source: "executor", ObservedAt: time.Now().UTC()}})
	if strings.Contains(string(observation), "-3") {
		t.Fatalf("负数观察未清理: %s", observation)
	}
	if !strings.Contains(string(media), `"available":false`) {
		t.Fatal("观察被当成媒体证据")
	}
}

func TestIdempotencyKeyAndSafeCode(t *testing.T) {
	if validIdempotencyKey("short") || !validIdempotencyKey("12345678") ||
		validIdempotencyKey(strings.Repeat("x", 129)) {
		t.Fatal("幂等键长度校验错误")
	}
	if safeCode("rate_limited") != "rate_limited" || safeCode("bad code") != "unknown" ||
		safeCode("") != "unknown" {
		t.Fatal("安全错误码清洗错误")
	}
}

func TestIdentityFingerprintValidation(t *testing.T) {
	valid := strings.Repeat("a", 64)
	service := &Service{opts: Options{IdentityFingerprint: func(namespace, subject string) string {
		if subject == "same" {
			return "same"
		}
		return valid
	}}}
	if value, err := service.identityFingerprint("ns", "subject"); err != nil || value != valid {
		t.Fatalf("有效指纹被拒绝: %v", err)
	}
	if _, err := service.identityFingerprint("ns", "same"); err == nil {
		t.Fatal("与主体相同的指纹被接受")
	}
	tooShort := &Service{opts: Options{IdentityFingerprint: func(string, string) string { return "short" }}}
	if _, err := tooShort.identityFingerprint("ns", "subject"); err == nil {
		t.Fatal("过短指纹被接受")
	}
	missing := &Service{opts: Options{}}
	if _, err := missing.identityFingerprint("ns", "subject"); err == nil {
		t.Fatal("缺少指纹函数仍接受身份")
	}
}

type staticPlatform struct{ capabilities PlatformCapabilities }

func (p staticPlatform) Capabilities() PlatformCapabilities { return p.capabilities }
func (p staticPlatform) ListCourses(context.Context, []byte) ([]PlatformCourse, error) {
	return nil, nil
}
func (p staticPlatform) DiscoverCourse(context.Context, []byte, PlatformCourseRef) (PlatformCourseSnapshot, error) {
	return PlatformCourseSnapshot{}, nil
}
func (p staticPlatform) CheckTask(context.Context, []byte, PlatformTaskRef) (PlatformCheck, error) {
	return PlatformCheck{}, nil
}

type staticVerifier struct{}

func (staticVerifier) Verify(context.Context, VerificationRequest) (PlatformCheck, error) {
	return PlatformCheck{Status: CheckUnconfirmed}, nil
}

type staticExecutor struct{ capabilities ExecutorCapabilities }

func (e staticExecutor) Capabilities() ExecutorCapabilities { return e.capabilities }
func (e staticExecutor) Execute(context.Context, ExecutionRequest) (ExecutionResult, error) {
	return ExecutionResult{}, nil
}

func TestExecutionCapabilityRequiresReadyComponents(t *testing.T) {
	resolver := SessionResolverFunc(func(context.Context, string) ([]byte, error) { return []byte("s"), nil })
	platform := staticPlatform{capabilities: PlatformCapabilities{Adapter: "fake", CourseRead: true, TaskCheck: true}}
	executor := staticExecutor{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true, TaskKinds: []string{"video"}}}
	base := Options{Platform: platform, Executor: executor, Verifier: staticVerifier{}, Sessions: resolver, ExecutionEnabled: true}
	service := &Service{opts: base}
	if !service.Capabilities().ExecutionEnabled {
		t.Fatal("所有组件就绪时执行能力未开启")
	}
	cases := []Options{
		{Platform: platform, Executor: executor, Verifier: staticVerifier{}, Sessions: resolver, ExecutionEnabled: false},
		{Platform: platform, Executor: staticExecutor{capabilities: ExecutorCapabilities{Name: "fake", Enabled: false}}, Verifier: staticVerifier{}, Sessions: resolver, ExecutionEnabled: true},
		{Executor: executor, Sessions: resolver, ExecutionEnabled: true},
		{Platform: platform, Executor: executor, Verifier: staticVerifier{}, ExecutionEnabled: true},
	}
	for index, options := range cases {
		if (&Service{opts: options}).Capabilities().ExecutionEnabled {
			t.Fatalf("缺少必要组件时执行能力仍为 true: %d", index)
		}
	}
}
