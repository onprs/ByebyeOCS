//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/postgres"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
)

// 本测试按公开安装后的真实顺序执行维护命令：config init、config check、
// migrate up、admin create、doctor 与 serve，用于发现配置接线或启动断路。
func TestMaintenanceCommandChain(t *testing.T) {
	db := testsupport.NewUnmigratedDatabase(t)
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	dsnPath := filepath.Join(directory, "secrets", "database.dsn")
	passwordPath := filepath.Join(directory, "secrets", "admin-password")
	totpPath := filepath.Join(directory, "secrets", "admin-totp.json")
	adminAssets := filepath.Join(directory, "admin-assets")
	clientAssets := filepath.Join(directory, "client-assets")
	for _, path := range []string{adminAssets, clientAssets} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "index.html"), []byte("<html>app</html>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(passwordPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordPath, []byte("correct horse battery staple\n"), 0600); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) string {
		t.Helper()
		var out, logs bytes.Buffer
		if err := Run(context.Background(), args, noEnvironment, &out, &logs); err != nil {
			t.Fatalf("%v 失败：%v（日志：%s）", args, err, logs.String())
		}
		return out.String()
	}

	run("config", "init", "--output", configPath,
		"--user-origin", "https://learn.example.test", "--admin-origin", "https://admin.example.test",
		"--data-dir", filepath.Join(directory, "data"),
		"--admin-assets-dir", adminAssets, "--client-assets-dir", clientAssets,
		"--database-dsn-file", dsnPath)
	if err := os.MkdirAll(filepath.Dir(dsnPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dsnPath, []byte(scratchDSN(t, db)), 0600); err != nil {
		t.Fatal(err)
	}
	run("config", "check", "--config", configPath)

	// 未迁移时 doctor 与 serve 必须明确拒绝，而不是在运行期半可用。
	if err := Run(context.Background(), []string{"doctor", "--config", configPath}, noEnvironment, io.Discard, io.Discard); err == nil {
		t.Fatal("未迁移数据库被 doctor 接受")
	}
	run("migrate", "up", "--config", configPath)
	run("doctor", "--config", configPath)
	if status := run("migrate", "status", "--config", configPath); !strings.Contains(status, `"ready":true`) {
		t.Fatalf("迁移状态未就绪：%s", status)
	}

	created := run("admin", "create", "--config", configPath, "--username", "owner",
		"--password-file", passwordPath, "--totp-output-file", totpPath)
	if !strings.Contains(created, `"status":"created"`) {
		t.Fatalf("管理员创建输出异常：%s", created)
	}
	var enrollment struct {
		Username string `json:"username"`
		Secret   string `json:"totp_secret"`
	}
	data, err := os.ReadFile(totpPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &enrollment); err != nil || enrollment.Username != "owner" || enrollment.Secret == "" {
		t.Fatalf("TOTP 交付文件无效：%v", err)
	}

	// 同一管理员不能静默重复创建。
	if err := Run(context.Background(), []string{"admin", "create", "--config", configPath, "--username", "owner",
		"--password-file", passwordPath, "--totp-secret-file", totpPath}, noEnvironment, io.Discard, io.Discard); err == nil {
		t.Fatal("重复管理员被接受")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	userAddress, adminAddress := freeAddress(t), freeAddress(t)
	go func() {
		done <- Run(ctx, []string{"serve", "--config", configPath, "--user-listen", userAddress,
			"--admin-listen", adminAddress, "--log-level", "error"}, noEnvironment, io.Discard, io.Discard)
	}()
	for _, address := range []string{userAddress, adminAddress} {
		waitListening(t, address)
	}
	for _, target := range []struct {
		address, path string
	}{
		{adminAddress, "/"},
		{userAddress, "/"},
		{adminAddress, "/health/live"},
		{adminAddress, "/health/ready"},
		{adminAddress, "/admin/api/v1/capabilities"},
		{userAddress, "/api/v1/capabilities"},
	} {
		response, err := http.Get("http://" + target.address + target.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s 返回 %d：%s", target.path, response.StatusCode, body)
		}
	}
	// 支付回调只挂在管理员入口；用户入口必须拒绝而不是回落到静态页面。
	response, err := http.Post("http://"+userAddress+"/payments/callbacks/zpay", "application/x-www-form-urlencoded", strings.NewReader("pid=x"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("用户入口错误地接受支付回调：%d", response.StatusCode)
	}
	// 管理员入口必须真正到达回调处理；未配置渠道时返回 503 重试而不是 404。
	response, err = http.Post("http://"+adminAddress+"/payments/callbacks/zpay", "application/x-www-form-urlencoded", strings.NewReader("pid=x"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("管理员入口未接通支付回调：%d", response.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("正常停止返回错误：%v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("停止服务超时")
	}

	// 重启必须能重新完成初始化并复用同一迁移；端口应在停止后释放。
	restartCtx, stopRestart := context.WithCancel(context.Background())
	defer stopRestart()
	restarted := make(chan error, 1)
	go func() {
		restarted <- Run(restartCtx, []string{"serve", "--config", configPath, "--user-listen", userAddress,
			"--admin-listen", adminAddress, "--log-level", "error"}, noEnvironment, io.Discard, io.Discard)
	}()
	for _, address := range []string{userAddress, adminAddress} {
		waitListening(t, address)
	}
	ready, err := http.Get("http://" + adminAddress + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusOK {
		t.Fatalf("重启后未就绪：%d", ready.StatusCode)
	}
	stopRestart()
	select {
	case err := <-restarted:
		if err != nil {
			t.Fatalf("重启后停止返回错误：%v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("重启后停止服务超时")
	}
}

// scratchDSN 把测试连接文件指向本次临时数据库，避免在基础实例里创建业务表。
func scratchDSN(t *testing.T, db *postgres.Database) string {
	t.Helper()
	path := os.Getenv("BYEBYEOCS_TEST_DATABASE_DSN_FILE")
	cfg, err := config.Load("", noEnvironment, config.Overrides{DSNFile: &path})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := url.Parse(cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	connection.Path = "/" + db.Pool().Config().ConnConfig.Database
	connection.RawPath = ""
	return connection.String()
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func waitListening(t *testing.T, address string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			connection.Close()
			return
		}
	}
	t.Fatalf("服务未监听：%s", address)
}
