package auth

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 展示字段按字符截断：长中文不能被切成无效 UTF-8（否则 PostgreSQL 写入失败），
// 控制字符仍需清理，稳定主体字段不受影响。
func TestSanitizeDisplayKeepsValidUTF8Boundary(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		limit     int
		wantRunes int
		contains  string
		absent    string
	}{
		{name: "长中文姓名", input: strings.Repeat("张三丰", 50), limit: 128, wantRunes: 128, contains: "张三丰"},
		{name: "长中文学校", input: strings.Repeat("示例实验学校", 40), limit: 190, wantRunes: 190, contains: "示例实验学校"},
		{name: "混合中英文", input: strings.Repeat("实验中学A", 60), limit: 128, wantRunes: 128},
		{name: "边界内不截断", input: strings.Repeat("学", 128), limit: 128, wantRunes: 128},
		{name: "多一字符截断", input: strings.Repeat("学", 129), limit: 128, wantRunes: 128},
		{name: "清理控制字符", input: "张\u0000三\u0007丰\n学校\t部", limit: 128, wantRunes: 8,
			contains: "张三丰 学校 部", absent: "\u0000"},
		{name: "首尾空白", input: "  张三  ", limit: 128, wantRunes: 2, contains: "张三"},
		{name: "无效 UTF-8 字节", input: strings.Repeat("学", 40) + "\xff\xfe", limit: 128, wantRunes: 42},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := sanitizeDisplay(item.input, item.limit)
			if !utf8.ValidString(got) {
				t.Fatalf("截断产生了无效 UTF-8: %q", got)
			}
			if runes := utf8.RuneCountInString(got); runes != item.wantRunes {
				t.Fatalf("截断后字符数为 %d，期望 %d: %q", runes, item.wantRunes, got)
			}
			if len(got) > 4*item.limit+16 {
				t.Fatalf("截断后字节数异常: %d", len(got))
			}
			if item.contains != "" && !strings.Contains(got, item.contains) {
				t.Fatalf("截断结果缺少期望内容 %q: %q", item.contains, got)
			}
			if item.absent != "" && strings.Contains(got, item.absent) {
				t.Fatalf("截断结果仍包含控制字符: %q", got)
			}
		})
	}
}
