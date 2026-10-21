package auth

import (
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }

func TestValidatePreferencePatch(t *testing.T) {
	if err := validatePreferencePatch(preferencesPatch{}); err != nil {
		t.Fatalf("空补丁被拒绝：%v", err)
	}
	valid := preferencesPatch{ColorScheme: stringPointer("grove"), ThemeMode: stringPointer("dark")}
	if err := validatePreferencePatch(valid); err != nil {
		t.Fatalf("有效补丁被拒绝：%v", err)
	}
	invalidColor := preferencesPatch{ColorScheme: stringPointer("neon")}
	if err := validatePreferencePatch(invalidColor); err == nil {
		t.Fatal("未知配色被接受")
	} else if code, _ := fault.Describe(err); code != "validation_failed" {
		t.Fatalf("未知配色返回错误码 %s", code)
	}
	invalidMode := preferencesPatch{ThemeMode: stringPointer("auto")}
	if err := validatePreferencePatch(invalidMode); err == nil {
		t.Fatal("未知外观模式被接受")
	}
}

func TestPreferencePatchMerge(t *testing.T) {
	base := defaultPreferences()
	if base.ColorScheme != "graphite" || base.ThemeMode != "system" || !base.TaskAttention || !base.WalletChanges {
		t.Fatalf("默认偏好不符合石墨/system 基线：%+v", base)
	}
	next := preferencesPatch{
		ColorScheme: stringPointer("indigo"),
		Notifications: &preferenceNotificationsPatch{
			WalletChanges: boolPointer(false),
		},
	}.apply(base)
	if next.ColorScheme != "indigo" || next.ThemeMode != "system" {
		t.Fatalf("外观字段合并错误：%+v", next)
	}
	if !next.TaskAttention || next.WalletChanges {
		t.Fatalf("通知字段合并错误：%+v", next)
	}
	if base.ColorScheme != "graphite" {
		t.Fatal("补丁修改了原始记录")
	}
}

func TestPreferenceViewShape(t *testing.T) {
	view := preferencesRecord{ColorScheme: "grove", ThemeMode: "light", TaskAttention: false, WalletChanges: true, Version: 4}.view()
	if view.Version != 4 || view.Notifications.TaskAttention || !view.Notifications.WalletChanges {
		t.Fatalf("偏好响应结构错误：%+v", view)
	}
}
