package chaoxing

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOptionalChineseProfileFieldsKeepValidUTF8(t *testing.T) {
	document := map[string]any{"school": strings.Repeat("大学", 100)}
	value := probeOptionalString(document, "school", 128)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) != 128 {
		t.Fatal("长中文学校名称截断为非法UTF8")
	}
}
