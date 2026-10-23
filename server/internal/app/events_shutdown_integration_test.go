//go:build integration

package app

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/events"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/transport/httpapi"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestGracefulShutdownClosesActiveEventStream(t *testing.T) {
	pool := testsupport.NewPool(t)
	stream := events.New(pool)
	mux := http.NewServeMux()
	stream.Register(mux)
	actor := web.Actor{ID: web.NewID(), Kind: "user"}
	business := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(web.WithActor(r.Context(), actor)))
	})
	cfg := config.Defaults().Server
	cfg.UserListen, cfg.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	cfg.ShutdownTimeout = 500 * time.Millisecond
	started := make(chan map[string]any, 1)
	logger := slog.New(slog.NewJSONHandler(startupWriter{events: started}, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamDone := make(chan error, 1)
	go func() { streamDone <- stream.Run(ctx) }()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- ServeWithOptions(ctx, cfg, readyCheck{}, logger, HTTPOptions{User: httpapi.Options{Business: business}})
	}()
	var address string
	select {
	case event := <-started:
		address = event["user_listen"].(string)
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP未启动")
	}
	requestCtx, stopRequest := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopRequest()
	request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+address+"/api/v1/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, "connected") {
		t.Fatalf("事件流未建立: %q %v", line, err)
	}
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("活动SSE导致停止超时: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP退出未释放SSE")
	}
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("事件服务未退出")
	}
}
