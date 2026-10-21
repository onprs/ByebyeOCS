// Package app 负责服务装配、双端监听与受控生命周期。
package app

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/transport/httpapi"
)

type readinessGate struct {
	ready atomic.Bool
	check httpapi.Readiness
}

func (g *readinessGate) Check(ctx context.Context) error {
	if !g.ready.Load() {
		return fault.New("service_stopping", "服务正在停止")
	}
	return g.check.Check(ctx)
}

type HTTPOptions struct {
	User  httpapi.Options
	Admin httpapi.Options
}

func Serve(ctx context.Context, cfg config.Server, check httpapi.Readiness, logger *slog.Logger) error {
	return ServeWithOptions(ctx, cfg, check, logger, HTTPOptions{})
}

func ServeWithOptions(ctx context.Context, cfg config.Server, check httpapi.Readiness, logger *slog.Logger, options HTTPOptions) error {
	if ctx.Err() != nil {
		return nil
	}
	userListener, err := net.Listen("tcp", cfg.UserListen)
	if err != nil {
		return fault.New("user_listen_failed", "无法监听用户端地址，请检查端口占用")
	}
	defer userListener.Close()
	adminListener, err := net.Listen("tcp", cfg.AdminListen)
	if err != nil {
		return fault.New("admin_listen_failed", "无法监听管理员地址，请检查端口占用")
	}
	defer adminListener.Close()

	gate := &readinessGate{check: check}
	gate.ready.Store(true)
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	makeServer := func(handler http.Handler) *http.Server {
		return &http.Server{
			Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
			MaxHeaderBytes: 16 * 1024, ErrorLog: log.New(io.Discard, "", 0),
			BaseContext: func(net.Listener) context.Context { return requestCtx },
		}
	}
	userServer := makeServer(httpapi.NewUserHandler(logger, options.User))
	adminServer := makeServer(httpapi.NewAdminHandler(gate, logger, options.Admin))
	results := make(chan error, 2)
	go func() { results <- userServer.Serve(userListener) }()
	go func() { results <- adminServer.Serve(adminListener) }()
	logger.Info("基础服务已启动", "user_listen", userListener.Addr().String(), "admin_listen", adminListener.Addr().String())
	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-results:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = fault.New("http_serve_failed", "HTTP 服务意外停止")
		}
	}
	gate.ready.Store(false)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	var wait sync.WaitGroup
	var timedOut atomic.Bool
	for _, server := range []*http.Server{userServer, adminServer} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := server.Shutdown(shutdownCtx); err != nil {
				timedOut.Store(true)
				cancelRequests()
				_ = server.Close()
			}
		}()
	}
	wait.Wait()
	if serveErr != nil {
		return serveErr
	}
	if timedOut.Load() {
		return fault.New("shutdown_timeout", "服务停止超时，连接已关闭")
	}
	logger.Info("基础服务已停止")
	return nil
}
