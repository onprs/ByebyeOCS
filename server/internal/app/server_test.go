package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

type readyCheck struct{}

func (readyCheck) Check(context.Context) error { return nil }

type startupWriter struct{ events chan map[string]any }

func (w startupWriter) Write(data []byte) (int, error) {
	var event map[string]any
	if json.Unmarshal(data, &event) == nil && event["msg"] == "基础服务已启动" {
		w.events <- event
	}
	return len(data), nil
}

func TestDualListenersAndGracefulShutdown(t *testing.T) {
	cfg := config.Defaults().Server
	cfg.UserListen, cfg.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	events := make(chan map[string]any, 1)
	logger := slog.New(slog.NewJSONHandler(startupWriter{events: events}, nil))
	ctx, cancel := context.WithCancel(context.Background())
	stopped, result := make(chan struct{}), make(chan error, 1)
	go func() { result <- Serve(ctx, cfg, readyCheck{}, logger); close(stopped) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Error("测试服务未停止")
		}
	})
	var event map[string]any
	select {
	case event = <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("服务未报告启动")
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	for _, test := range []struct {
		listener, path string
		status         int
	}{
		{"user_listen", "/api/v1/capabilities", 200},
		{"user_listen", "/admin/api/v1/capabilities", 404},
		{"user_listen", "/health/ready", 404},
		{"admin_listen", "/admin/api/v1/capabilities", 200},
		{"admin_listen", "/api/v1/capabilities", 404},
		{"admin_listen", "/health/ready", 200},
	} {
		response, err := client.Get("http://" + event[test.listener].(string) + test.path)
		if err != nil {
			t.Fatal("本地接口不可访问")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("%s 状态码错误", test.path)
		}
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("优雅停止超时")
	}
	for _, key := range []string{"user_listen", "admin_listen"} {
		listener, err := net.Listen("tcp", event[key].(string))
		if err != nil {
			t.Fatal("停止后没有释放监听端口")
		}
		listener.Close()
	}
}

func TestListenerFailureReleasesOtherPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := available.Addr().String()
	available.Close()
	cfg := config.Defaults().Server
	cfg.UserListen, cfg.AdminListen = address, occupied.Addr().String()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	err = Serve(context.Background(), cfg, readyCheck{}, logger)
	code, _ := fault.Describe(err)
	if code != "admin_listen_failed" {
		t.Fatal("未报告管理员端口冲突")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("管理员监听失败后用户端口未释放")
	}
	listener.Close()
	cfg.UserListen = occupied.Addr().String()
	err = Serve(context.Background(), cfg, readyCheck{}, logger)
	code, _ = fault.Describe(err)
	if code != "user_listen_failed" {
		t.Fatal("未报告用户端口冲突")
	}
}

func TestCancelledContextDoesNotListen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, config.Defaults().Server, readyCheck{}, slog.Default()); err != nil {
		t.Fatal(err)
	}
}
