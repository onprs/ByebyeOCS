//go:build integration

package chaoxing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type nativeWorkAnswers struct{}

func (nativeWorkAnswers) AnswerWorkQuestion(_ context.Context, _, _ string, _ learning.QuestionPolicy, question learning.WorkQuestion) (learning.WorkQueryResult, error) {
	answers := map[string]string{"110001": "A", "110002": "A#C", "110003": "正确", "110004": "第一空#第二空", "110005": "简答原文第一行\n简答原文第二行"}
	return learning.WorkQueryResult{QueryID: "query_" + question.ID, Answer: answers[question.ID]}, nil
}

// 使用正式原生平台适配器、实际调度器与PostgreSQL，避免只验证一个总是成功的WorkPlatform桩。
func TestNativeQuizHTTPThroughSchedulerAndIndependentConfirmation(t *testing.T) {
	for _, confirm := range []bool{true, false} {
		t.Run(fmt.Sprintf("platform_confirmed=%v", confirm), func(t *testing.T) {
			pool := testsupport.NewPool(t)
			fixture := newWorkFixture(t)
			var mu sync.Mutex
			passed := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/courselist":
					fmt.Fprint(w, `<a href="https://mooc1.chaoxing.com/visit/stucoursemiddle?courseid=101&clazzid=202&cpi=303">课程</a><span class="course-name-x" title="原生协议课程"></span>`)
				case "/catalog":
					fmt.Fprint(w, `<script>toOld('101','5001','202')</script><div id="cur5001"><span class="catalog_points_yi">1</span></div>`)
				case "/study":
					fmt.Fprint(w, `<ul id="prev_tab"><li cardid="first"></li><li cardid="second"></li><li cardid="quiz"></li></ul>`)
				case "/cards":
					if r.URL.Query().Get("num") != "2" {
						fmt.Fprint(w, `mArg={"attachments":[]};`)
						return
					}
					mu.Lock()
					current := passed
					mu.Unlock()
					fmt.Fprintf(w, `mArg={"attachments":[{"jobid":"%s","type":"workid","isPassed":%t,"enc":"cardenc","property":{"name":"测验"}}],"defaults":{"knowledgeid":"5001","cpi":"303","ktoken":"ktoken1"}};`, workJobID, current)
				case "/submit":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if r.Form.Get("answer110001") != "A" || r.Form.Get("answer110002") != "AC" || r.Form.Get("answer110003") != "true" || r.Form.Get("answer1100041") != "第一空" || r.Form.Get("answer1100042") != "第二空" || !strings.Contains(r.Form.Get("answer110005"), "简答原文第二行") || r.Form.Get("pyFlag") != "" {
						t.Errorf("正式调度器未提交当前五题型答案: %v", r.Form)
						w.WriteHeader(422)
						return
					}
					fixture.handle(w, r)
					mu.Lock()
					passed = confirm
					mu.Unlock()
				default:
					fixture.handle(w, r)
				}
			}))
			defer server.Close()
			// fixture中的客户端与图片地址都使用当前受控TLS服务。
			fixture.server.Close()
			fixture.server = server
			platform := fixture.platform(t)
			platform.options.endpoints.Study = server.URL + "/study"
			executor, err := learning.NewAutomaticExecutor(pool, learning.AutomaticExecutorOptions{Enabled: true, Platform: platform, Answerer: nativeWorkAnswers{}})
			if err != nil {
				t.Fatal(err)
			}
			session := encodeTestSession(t)
			service, err := learning.New(pool, &learning.Options{Platform: platform, Verifier: platform, Executor: executor,
				Sessions: learning.SessionResolverFunc(func(context.Context, string) ([]byte, error) { return session, nil }),
				IdentityFingerprint: func(namespace, subject string) string {
					digest := sha256.Sum256([]byte("native-fixture/" + namespace + "/" + subject))
					return hex.EncodeToString(digest[:])
				}, QuestionsPolicyValidator: learning.QuestionsPolicyValidatorFunc(func(context.Context, string, string) error { return nil }),
				ExecutionEnabled: true, GlobalConcurrency: 1, AccountConcurrency: 1, LeaseTTL: 10 * time.Second, HeartbeatInterval: time.Second,
				PollInterval: 20 * time.Millisecond, VerificationInterval: 30 * time.Millisecond, RetryBase: 20 * time.Millisecond, MaxVerifications: 3})
			if err != nil {
				t.Fatal(err)
			}
			userID := web.NewID()
			if _, err := pool.Exec(context.Background(), `INSERT INTO public.users(id) VALUES($1)`, userID); err != nil {
				t.Fatal(err)
			}
			if err := service.BindAccount(context.Background(), userID, learning.AccountIdentity{Provider: "chaoxing", Namespace: "native-fixture", Subject: userID, VerifiedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()
			defer func() { cancel(); <-done }()
			actor := learning.Actor{ID: userID, Kind: "user"}
			if _, _, err := service.RequestCourseSync(ctx, actor); err != nil {
				t.Fatal(err)
			}
			var courseID string
			waitNativeCondition(t, func() bool {
				return pool.QueryRow(ctx, `SELECT id::text FROM public.learning_courses WHERE user_id=$1`, userID).Scan(&courseID) == nil
			})
			modelID := "qm_native_fixture"
			if _, err := service.CreatePlan(ctx, actor, learning.PlanInput{Name: "原生自动测验", CourseIDs: []string{courseID}, Scope: learning.PlanScopeInput{Kind: "all"}, TaskTypes: []string{"quiz"}, QuestionPolicy: learning.QuestionPolicyInput{Enabled: true, ModelOfferingID: &modelID, AnswerMode: "cache_first"}}); err != nil {
				t.Fatal(err)
			}
			want := learning.StateConfirmed
			if !confirm {
				want = learning.StateNeedsAttention
			}
			var state, platformStatus string
			waitNativeCondition(t, func() bool {
				return pool.QueryRow(ctx, `SELECT state,platform_status FROM public.learning_tasks WHERE user_id=$1`, userID).Scan(&state, &platformStatus) == nil && state == want
			})
			fixture.mu.Lock()
			posts := fixture.postCount
			fixture.mu.Unlock()
			if posts != 1 || confirm && platformStatus != learning.CheckConfirmed || !confirm && platformStatus == learning.CheckConfirmed {
				t.Fatalf("提交替代了任务点确认或发生重复提交: state=%s, check=%s, posts=%d", state, platformStatus, posts)
			}
			var index int
			if err := pool.QueryRow(ctx, `SELECT a.card_index FROM public.learning_tasks t JOIN public.learning_attachments a ON a.id=t.attachment_id WHERE t.user_id=$1`, userID).Scan(&index); err != nil || index != 2 {
				t.Fatalf("实际卡片页码未持久化: %d / %v", index, err)
			}
		})
	}
}

func waitNativeCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("原生HTTP调度条件未在时限内满足")
}
