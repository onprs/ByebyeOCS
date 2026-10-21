package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyCommands(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key == "BYEBYEOCS_DATABASE_DSN" {
			return "postgres://service:private-test-value@127.0.0.1:1/test?sslmode=disable", true
		}
		return "", false
	}
	for _, args := range [][]string{nil, {"help"}, {"version"}, {"serve", "--help"}, {"config", "check"}} {
		var out, logs bytes.Buffer
		if err := Run(context.Background(), args, lookup, &out, &logs); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatal("维护命令没有输出")
		}
		if strings.Contains(out.String()+logs.String(), "private-test-value") {
			t.Fatal("维护命令泄露连接串")
		}
	}
}

func TestInvalidCommandsNeverEchoArguments(t *testing.T) {
	for _, args := range [][]string{
		{"private-test-value"}, {"serve", "--private-test-value"}, {"serve", "private-test-value"},
		{"migrate"}, {"migrate", "private-test-value"}, {"version", "private-test-value"},
	} {
		var out, logs bytes.Buffer
		err := Run(context.Background(), args, func(string) (string, bool) { return "", false }, &out, &logs)
		if err == nil {
			t.Fatal("错误命令被接受")
		}
		WriteError(&logs, err)
		if strings.Contains(out.String()+logs.String(), "private-test-value") {
			t.Fatal("命令错误回显原始参数")
		}
	}
	var logs bytes.Buffer
	WriteError(&logs, errors.New("private-test-value"))
	if strings.Contains(logs.String(), "private-test-value") {
		t.Fatal("未知异常未被脱敏")
	}
}

func TestConfigPathPriority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.yaml")
	if err := os.WriteFile(path, []byte("log:\n  level: warn\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lookup := func(key string) (string, bool) {
		switch key {
		case "BYEBYEOCS_CONFIG":
			return "missing-config", true
		case "BYEBYEOCS_DATABASE_DSN":
			return "postgres://service@127.0.0.1/test?sslmode=disable", true
		}
		return "", false
	}
	var out, logs bytes.Buffer
	if err := Run(context.Background(), []string{"config", "check", "--config", path, "--log-level", "error"}, lookup, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"log_level":"error"`) {
		t.Fatal("命令行未覆盖文件和环境变量")
	}
}
