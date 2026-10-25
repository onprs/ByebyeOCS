//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/questions"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 这些夹具只存在于集成测试二进制，验证真实调度、查询服务与账本接线。
type automaticWorkPlatform struct {
	fixturePlatform
	submitted atomic.Bool
	posts     atomic.Int64
	invalid   atomic.Bool
	unknown   bool
}

func (p *automaticWorkPlatform) DiscoverCourse(_ context.Context, _ []byte, course learning.PlatformCourseRef) (learning.PlatformCourseSnapshot, error) {
	return learning.PlatformCourseSnapshot{Course: course, Title: "自动测验课程", Adapter: "local-fixture",
		Chapters: []learning.PlatformChapter{{ExternalID: "fixture-chapter", Title: "章节测验",
			Attachments: []learning.PlatformAttachment{{ObjectID: "work-901", TaskPointID: "work-901", Kind: "quiz",
				Title: "章节测验", PassedKnown: true}}}}}, nil
}

func (p *automaticWorkPlatform) CheckTask(context.Context, []byte, learning.PlatformTaskRef) (learning.PlatformCheck, error) {
	status := learning.CheckUnconfirmed
	if p.submitted.Load() {
		status = learning.CheckConfirmed
	}
	return learning.PlatformCheck{Status: status, Passed: p.submitted.Load(), CheckedAt: time.Now().UTC(), Source: "local-work-fixture"}, nil
}

func automaticWorkSheet() learning.WorkSheet {
	digest := sha256.Sum256([]byte("isolated-automatic-work-sheet-v1"))
	options := []learning.WorkOption{{Label: "A", Value: "A", Text: "第一项"},
		{Label: "B", Value: "B", Text: "第二项"}, {Label: "C", Value: "C", Text: "第三项"}}
	return learning.WorkSheet{Fingerprint: hex.EncodeToString(digest[:]), Questions: []learning.WorkQuestion{
		{ID: "101", Type: "single", Title: "自动单选题", Options: options},
		{ID: "102", Type: "multiple", Title: "自动多选题", Options: options},
		{ID: "103", Type: "judgement", Title: "自动判断题"},
		{ID: "104", Type: "completion", Title: "自动填空题", Blanks: 2},
		{ID: "105", Type: "shortanswer", Title: "自动简答题"},
	}}
}

func (p *automaticWorkPlatform) ReadWork(context.Context, []byte, learning.PlatformTaskRef) (learning.WorkSheet, error) {
	sheet := automaticWorkSheet()
	sheet.Submitted = p.submitted.Load()
	return sheet, nil
}

func (p *automaticWorkPlatform) SubmitWork(ctx context.Context, _ []byte, _ learning.PlatformTaskRef, fingerprint string, answers []learning.WorkAnswer, beforePost func(context.Context) error) (learning.WorkSubmission, error) {
	if beforePost != nil {
		if err := beforePost(ctx); err != nil {
			return learning.WorkSubmission{}, err
		}
	}
	p.posts.Add(1)
	want := []learning.WorkAnswer{{QuestionID: "101", Values: []string{"B"}},
		{QuestionID: "102", Values: []string{"A", "C"}}, {QuestionID: "103", Values: []string{"true"}},
		{QuestionID: "104", Values: []string{"甲", "乙"}}, {QuestionID: "105", Values: []string{"完整简答内容"}}}
	if fingerprint != automaticWorkSheet().Fingerprint || !reflect.DeepEqual(answers, want) || p.submitted.Swap(true) {
		p.invalid.Store(true)
		return learning.WorkSubmission{}, learning.Fail(learning.FailureInvalidTask, "夹具检测到错误或重复提交")
	}
	if p.unknown {
		return learning.WorkSubmission{}, learning.Fail(learning.FailureSubmissionUnknown, "夹具模拟提交响应丢失")
	}
	return learning.WorkSubmission{Accepted: true}, nil
}

type automaticWorkFork struct {
	calls       atomic.Int64
	missingHold atomic.Int64
	before      atomic.Value
	invalid     bool
	incomplete  bool
	catalog     appCatalog
}

func (f *automaticWorkFork) Offerings(ctx context.Context) ([]questions.ForkOffering, error) {
	return f.catalog.Offerings(ctx)
}

func (f *automaticWorkFork) seedOffering(item questions.ForkOffering) { f.catalog.add(item) }

func (f *automaticWorkFork) Answer(ctx context.Context, request questions.ForkAnswerRequest, _ string) (questions.ForkAnswerResponse, error) {
	f.calls.Add(1)
	if check, ok := f.before.Load().(func(context.Context) bool); ok && !check(ctx) {
		f.missingHold.Add(1)
	}
	answer := map[string]string{"single": "B", "multiple": "C#A", "judgement": "对", "completion": "甲#乙", "shortanswer": "完整简答内容"}[request.Question.Type]
	if f.invalid {
		answer = "无法唯一确定"
	}
	input, output, total := int64(100), int64(50), int64(150)
	usage := questions.Usage{InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Complete: true,
		Calls: []questions.Call{{Slot: "answer", InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Status: "ok"}}}
	status := "answered"
	if f.incomplete {
		usage.Complete, usage.TotalTokens = false, nil
		status = "usage_incomplete"
	}
	return questions.ForkAnswerResponse{RequestID: request.RequestID, Status: status, Question: request.Question.Title,
		Answer: answer, Model: questions.ForkModel{OfferingID: request.ModelOfferingID, Capabilities: []string{"text"}},
		Mode: request.Mode, Usage: usage}, nil
}
func (*automaticWorkFork) TestModel(context.Context, string, string, bool) (questions.ForkTestResult, error) {
	return questions.ForkTestResult{}, nil
}
func (*automaticWorkFork) InvalidateCache(context.Context, string, string) (bool, error) {
	return true, nil
}

type automaticVideoFallback struct{}

func (automaticVideoFallback) Capabilities() learning.ExecutorCapabilities {
	return learning.ExecutorCapabilities{Name: "local-video-fixture", Enabled: true, TaskKinds: []string{"video"}, SafeLeaseRecovery: true}
}
func (automaticVideoFallback) Execute(context.Context, learning.ExecutionRequest) (learning.ExecutionResult, error) {
	return learning.ExecutionResult{}, learning.Fail(learning.FailureInvalidTask, "章节测验不应调用视频执行器")
}

func TestRuntimeAutomaticQuestionsSubmitWithoutConfirmation(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		unknown    bool
		invalid    bool
		incomplete bool
	}{
		{name: "五种题型直接提交"},
		{name: "响应丢失后自动核验", unknown: true},
		{name: "无效答案停止且不提交", invalid: true},
		{name: "用量缺失仍作答且不扣费", incomplete: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			platform := &automaticWorkPlatform{unknown: scenario.unknown}
			fork := &automaticWorkFork{invalid: scenario.invalid, incomplete: scenario.incomplete}
			fixture := newRuntimeFixture(t, fixtureOptions{platform: platform, executor: automaticVideoFallback{}, fork: fork, charging: true})
			client := fixtureClient()
			defer client.CloseIdleConnections()
			csrf, userID := loginFixtureUser(t, fixture, client, "learner-a")
			adminClient := fixtureClient()
			defer adminClient.CloseIdleConnections()
			code, _, session := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "",
				map[string]string{"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP()}, nil)
			if code != 200 {
				t.Fatal("管理员登录失败", code)
			}
			admin := session["admin"].(map[string]any)
			actor := web.Actor{ID: admin["id"].(string), Kind: "admin", Permissions: []string{"*"}, AuthenticatedAt: time.Now()}
			modelID := seedOffering(t, fork, "自动测验模型", "automatic-fixture-model",
				questions.CachePolicy{Allow: true, Scope: "user", TTLDays: 7},
				&questions.OfferingPricing{Version: "automatic-question-v1", InputMicrosPerMillionTokens: 1000000,
					OutputMicrosPerMillionTokens: 2000000, MicrosPerMinor: 10000, Rounding: "ceil",
					Rule: "tokens_x_micros_per_million_v1"}, true)
			ctx := context.Background()
			wallet, err := fixture.runtime.Ledger.GetWallet(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.runtime.Ledger.AdjustWallet(ctx, actor, "automatic-question-funds",
				ledger.WalletAdjustmentInput{WalletID: wallet.ID, Direction: "credit", AmountMinor: "1000", Reason: "隔离自动测验合成资金"}); err != nil {
				t.Fatal(err)
			}
			enabled := true
			if _, err := fixture.runtime.Ledger.UpdateBillingSettings(ctx, actor, 1, ledger.BillingPatch{TikuChargeEnabled: &enabled}); err != nil {
				t.Fatal(err)
			}
			fork.before.Store(func(ctx context.Context) bool {
				var amount int64
				return fixture.runtime.db.Pool().QueryRow(ctx, `SELECT reserved_minor FROM public.wallets WHERE user_id=$1`, userID).Scan(&amount) == nil && amount > 0
			})
			code, _, _ = requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/course-syncs", csrf, map[string]any{}, map[string]string{"Idempotency-Key": web.NewID()})
			if code != 202 {
				t.Fatal("课程同步未受理", code)
			}
			var courseID string
			waitAutomatic(t, 8*time.Second, func() bool {
				return fixture.runtime.db.Pool().QueryRow(ctx, `SELECT id::text FROM public.learning_courses WHERE user_id=$1`, userID).Scan(&courseID) == nil
			})
			code, _, plan := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/plans", csrf, map[string]any{
				"name": "全自动测验安排", "course_ids": []string{courseID}, "scope": map[string]string{"kind": "all"}, "task_types": []string{"quiz"},
				"question_policy": map[string]any{"enabled": true, "model_offering_id": modelID, "answer_mode": "always_llm"}}, nil)
			if code != 201 {
				t.Fatal("自动安排创建失败", code, plan)
			}
			policy := plan["question_policy"].(map[string]any)
			if policy["require_manual_confirmation"] != false || policy["execution_available"] != true || policy["submission_available"] != true {
				t.Fatal("自动执行能力或确认策略不正确", policy)
			}
			wantState := learning.StateConfirmed
			if scenario.invalid {
				wantState = learning.StateNeedsAttention
			}
			var state, taskID string
			waitAutomatic(t, 14*time.Second, func() bool {
				return fixture.runtime.db.Pool().QueryRow(ctx, `SELECT id::text,state FROM public.learning_tasks WHERE user_id=$1`, userID).Scan(&taskID, &state) == nil && state == wantState
			})
			wantCalls, wantPosts, wantBalance := int64(5), int64(1), "995"
			if scenario.invalid {
				wantCalls, wantPosts, wantBalance = 1, 0, "999"
			}
			if scenario.incomplete {
				wantBalance = "1000"
			}
			wallet, err = fixture.runtime.Ledger.GetWallet(ctx, userID)
			if err != nil || wallet.AvailableMinor != wantBalance || wallet.ReservedMinor != "0" || fork.calls.Load() != wantCalls || platform.posts.Load() != wantPosts || platform.invalid.Load() || fork.missingHold.Load() != 0 {
				t.Fatalf("自动提交或计量错误: state=%s wallet=%+v queries=%d posts=%d invalid=%t missing_hold=%d err=%v", state, wallet, fork.calls.Load(), platform.posts.Load(), platform.invalid.Load(), fork.missingHold.Load(), err)
			}
			if !scenario.invalid {
				var recordedModel string
				if err := fixture.runtime.db.Pool().QueryRow(ctx, `SELECT model_offering_id FROM public.learning_work_runs WHERE task_id=$1`, taskID).Scan(&recordedModel); err != nil || recordedModel != modelID {
					t.Fatal("执行没有使用冻结的公开模型", err)
				}
			}
		})
	}
}

func waitAutomatic(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("自动做题状态未在限定时间内完成")
		}
		<-ticker.C
	}
}
