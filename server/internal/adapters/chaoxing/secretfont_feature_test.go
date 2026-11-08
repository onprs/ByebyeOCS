package chaoxing

import (
	"os"
	"testing"
)

// 特征表用同一字体生成后必须能稳定还原字形，保证兜底路径可回归。
func TestSecretFeatureTableRoundTrip(t *testing.T) {
	payload, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	fontBytes, err := secretFontBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	font, err := parseSecretFont(fontBytes)
	if err != nil {
		t.Fatal(err)
	}
	chars := make([]rune, 0, 5)
	for code := range font.cmap {
		chars = append(chars, code)
		if len(chars) == 5 {
			break
		}
	}
	tableBytes, err := BuildSecretFeatureTable(fontBytes, chars)
	if err != nil {
		t.Fatal(err)
	}
	table, err := parseSecretFeatureTable(tableBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, char := range chars {
		gid, found := font.cmap[char]
		if !found {
			t.Fatalf("字体缺少 %c", char)
		}
		feature, ok := secretGlyphFeature(font, gid)
		if !ok {
			t.Fatalf("字形 %c 没有特征", char)
		}
		result := table.match(feature, secretFeatureMargin)
		if !result.found || result.glyph != char {
			t.Fatalf("兜底匹配错误: 期望 %c 实际 %c/%v", char, result.glyph, result.found)
		}
	}
}

// 旧签名索引缺失时由原版轮廓接管；没有可信参考时不能猜字。
func TestSecretFontDecoderFallsBackToReference(t *testing.T) {
	payload, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := newSecretFontDecoder(payload)
	if err != nil {
		t.Fatal(err)
	}
	decoder.index = map[string]rune{}
	code := rune('崤')
	got, status := decoder.resolve(code)
	if status != glyphResolved || got != '指' {
		t.Fatalf("没有由原版轮廓还原: %c/%v", got, status)
	}
	decoder.cache = map[rune]rune{}
	decoder.reference = nil
	if _, status := decoder.resolve(code); status == glyphResolved {
		t.Fatal("没有可信参考时不应命中")
	}
	if _, err := decoder.decodeText(string(code)); err == nil {
		t.Fatal("无法解码时必须返回错误")
	}
}
