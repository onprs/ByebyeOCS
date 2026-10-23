//go:build integration

package chaoxing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type platformWithoutSessionScope struct{ learning.Platform }

func TestCourseSyncKeepsCookieFromListThroughDiscovery(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprintf("scoped=%v", scoped), func(t *testing.T) {
			pool := testsupport.NewPool(t)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/courselist" {
					http.SetCookie(w, &http.Cookie{Name: "course_ticket", Value: "controlled-ticket", Path: "/", Secure: true})
					fmt.Fprint(w, `<a href="/visit/stucoursemiddle?courseid=101&clazzid=202&cpi=303">进入</a>`)
					return
				}
				cookie, err := r.Cookie("course_ticket")
				if err != nil || cookie.Value != "controlled-ticket" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				switch r.URL.Path {
				case "/catalog":
					fmt.Fprint(w, `<script>toOld('101','5001','202')</script><div id="cur5001"><span class="catalog_points_yi">1</span></div>`)
				case "/cards":
					fmt.Fprint(w, `mArg={"attachments":[{"objectId":"v1","jobid":"9001","type":"video","isPassed":false,"attDuration":60,"playTime":0}]};`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			native := testAdapter(t, server)
			var platform learning.Platform = native
			if !scoped {
				platform = platformWithoutSessionScope{native}
			}
			session := encodeTestSession(t)
			original := append([]byte(nil), session...)
			service, err := learning.New(pool, &learning.Options{
				Platform: platform, Sessions: learning.SessionResolverFunc(func(context.Context, string) ([]byte, error) { return session, nil }),
				IdentityFingerprint: func(namespace, subject string) string {
					sum := sha256.Sum256([]byte(namespace + "/" + subject))
					return hex.EncodeToString(sum[:])
				}, PollInterval: 20 * time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			user := web.NewID()
			if _, err := pool.Exec(context.Background(), `INSERT INTO public.users(id) VALUES($1)`, user); err != nil {
				t.Fatal(err)
			}
			if err := service.BindAccount(context.Background(), user, learning.AccountIdentity{Provider: "chaoxing", Namespace: "cookie-fixture", Subject: user}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()
			defer func() { cancel(); <-done }()
			sync, _, err := service.RequestCourseSync(ctx, learning.Actor{Kind: "user", ID: user})
			if err != nil {
				t.Fatal(err)
			}
			var status, code string
			waitNativeCondition(t, func() bool {
				return pool.QueryRow(ctx, `SELECT status,error_code FROM public.learning_course_syncs WHERE id=$1`, sync.ID).Scan(&status, &code) == nil && status != "queued" && status != "running"
			})
			if scoped && status != "succeeded" || !scoped && (status != "needs_attention" || code != "forbidden") {
				t.Fatalf("Cookie连续性结果错误：%s/%s", status, code)
			}
			if !bytes.Equal(session, original) {
				t.Fatal("操作内Cookie轮换改写持久化会话")
			}
		})
	}
}
