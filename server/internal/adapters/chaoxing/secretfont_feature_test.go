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

// 精确索引缺失时必须由特征兜底接管；置信度不足时仍然失败。
func TestSecretFontDecoderFallsBackToFeatures(t *testing.T) {
	payload, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := newSecretFontDecoder(payload)
	if err != nil {
		t.Fatal(err)
	}
	code := []rune("崤")[0]
	signature, found := decoder.signatures[code]
	if !found {
		t.Fatalf("夹具缺少码点 %c", code)
	}
	want := decoder.index[signature]
	delete(decoder.index, signature)
	decoder.cache = map[rune]rune{}
	got, ok := decoder.resolve(code)
	if !ok || got != want {
		t.Fatalf("索引缺失后没有由特征兜底还原: %c/%v（期望 %c）", got, ok, want)
	}
	// 没有特征表时必须失败，不能返回错字。
	decoder.cache = map[rune]rune{}
	decoder.features = nil
	if _, ok := decoder.resolve(code); ok {
		t.Fatal("没有特征表时不应命中")
	}
	if _, err := decoder.decodeText(string(code)); err == nil {
		t.Fatal("无法解码时必须返回错误")
	}
}
