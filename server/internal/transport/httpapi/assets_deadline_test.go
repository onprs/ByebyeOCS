package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type delayedDownloadWriter struct {
	http.ResponseWriter
	once sync.Once
}

func (w *delayedDownloadWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { time.Sleep(250 * time.Millisecond) })
	return w.ResponseWriter.Write(data)
}

func (w *delayedDownloadWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestStaticDownloadCanOutliveOrdinaryAPIWriteTimeout(t *testing.T) {
	root := t.TempDir()
	body := "受控静态资源内容"
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, item := range []struct {
		path     string
		complete bool
	}{
		{"/index.html", true},
		{"/api/v1/download-deadline-control", false},
	} {
		t.Run(item.path, func(t *testing.T) {
			handler := NewUserHandler(logger, Options{AssetsDir: root, Business: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ordinary api")) })})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(&delayedDownloadWriter{ResponseWriter: w}, r)
			}))
			server.Config.WriteTimeout = 100 * time.Millisecond
			server.Start()
			defer server.Close()
			client := server.Client()
			client.Timeout = 2 * time.Second
			response, err := client.Get(server.URL + item.path)
			if !item.complete {
				if err == nil {
					response.Body.Close()
					t.Fatal("普通API的输出期限被静态资源例外延长")
				}
				return
			}
			if err != nil {
				t.Fatalf("合法静态下载被短API输出期限中断：%v", err)
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil || string(data) != body || response.StatusCode != http.StatusOK {
				t.Fatalf("静态下载未完整交付：%d/%q/%v", response.StatusCode, data, err)
			}
		})
	}
}
