package chaoxing

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSecretFontReferenceOverridesIncorrectLegacyLabels(t *testing.T) {
	mappings := map[rune]rune{0xe101: '光', 0xe102: '格', 0xe103: '借'}
	font := testSecretReferenceFont(t, mappings)
	decoder, err := newSecretFontDecoder(inlineSecretFontPayload(nil, font))
	if err != nil {
		t.Fatal(err)
	}
	decoder.index = map[string]rune{}
	wrong := map[rune]rune{'光': '凫', '格': '质', '借': '惜'}
	for code, value := range mappings {
		signature, err := decoder.font.signature(decoder.font.cmap[code])
		if err != nil {
			t.Fatal(err)
		}
		decoder.index[signature] = wrong[value]
		got, status := decoder.resolve(code)
		if status != glyphResolved || got != value {
			t.Fatalf("旧标签覆盖了原版轮廓: %c/%v", got, status)
		}
	}
}

func TestSecretFontInvalidLocaIsNotWhitespace(t *testing.T) {
	font := testSecretReferenceFont(t, map[rune]rune{0xe101: '龘'})
	decoder, err := newSecretFontDecoder(inlineSecretFontPayload(nil, font))
	if err != nil {
		t.Fatal(err)
	}
	gid := decoder.font.cmap[0xe101]
	invalid := uint32(len(decoder.font.glyf) + 8)
	decoder.font.loca[gid] = invalid
	decoder.font.loca[gid+1] = invalid
	if _, status := decoder.resolve(0xe101); status == glyphEmpty || status == glyphResolved {
		t.Fatal("损坏字形被当成空白或已解码")
	}
	if _, err := decoder.decodeText("\ue101"); err == nil {
		t.Fatal("使用损坏轮廓必须失败")
	}
}

func TestSecretFontCmap12RejectsInvalidUnicodeAndZeroGlyph(t *testing.T) {
	for _, group := range [][3]uint32{{0xfffffff0, 0xffffffff, 1}, {0xd800, 0xd801, 1}, {0xe100, 0xe100, 0}} {
		data := make([]byte, 28)
		binary.BigEndian.PutUint16(data, 12)
		binary.BigEndian.PutUint32(data[4:], 28)
		binary.BigEndian.PutUint32(data[12:], 1)
		for index, value := range group {
			binary.BigEndian.PutUint32(data[16+4*index:], value)
		}
		mapping := map[rune]uint16{}
		if err := parseSecretFontCmap12(data, 0, mapping); err != nil {
			t.Fatal(err)
		}
		if len(mapping) != 0 {
			t.Fatalf("非法 Unicode 或 .notdef 被接受: %+v", mapping)
		}
	}
}

func TestSecretFontTriesSupportedSourceAfterUnsupportedFormat(t *testing.T) {
	font := testSecretReferenceFont(t, map[rune]rune{0xe101: '龘'})
	payload := inlineSecretFontPayload(nil, font)
	payload = bytes.Replace(payload, []byte("src:url("), []byte("src:url('data:font/woff2;base64,d09GMgAAAAAAAAAAAA=='),url("), 1)
	decoder, err := newSecretFontDecoder(payload)
	if err != nil {
		t.Fatal(err)
	}
	decoder.index = map[string]rune{}
	if got, err := decoder.decodeText("\ue101"); err != nil || got != "龘" {
		t.Fatalf("未回退到可解析字体源: %q/%v", got, err)
	}
}
