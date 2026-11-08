package chaoxing

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestSecretFontReferenceDecodesUnseenGlyphsAndPermutedCodes(t *testing.T) {
	// 这些字不来自已抓取课程；字体由原版轮廓生成，码点每次置换。
	for _, mappings := range []map[rune]rune{
		{0xe101: '龘', 0xe102: '䨻', 0x5d24: '𠮷'},
		{0xe101: '𠮷', 0xe102: '龘', 0x5d24: '䨻'},
	} {
		font := testSecretReferenceFont(t, mappings)
		decoder, err := newSecretFontDecoder(inlineSecretFontPayload([]byte(`<p class="font-cxsecret"></p>`), font))
		if err != nil {
			t.Fatal(err)
		}
		decoder.index = map[string]rune{}
		for code, want := range mappings {
			got, err := decoder.decodeText(string(code))
			if err != nil || got != string(want) {
				t.Fatalf("%U: 得到 %q/%v，期望 %c", code, got, err, want)
			}
		}
	}
}

func TestSecretFontReferenceNormalizesStandardAliases(t *testing.T) {
	for input, want := range map[rune]rune{'⻔': '门', '⺠': '民', '⻓': '长', '㆔': '三', '⼀': '一'} {
		if got := normalizeSecretReferenceRune(input); got != want {
			t.Errorf("%c -> %c，期望 %c", input, got, want)
		}
	}
	for _, code := range []rune{'Ａ', 'A', '一', '—', 'O', '0'} {
		if got := normalizeSecretReferenceRune(code); got != code {
			t.Errorf("不应归并不同符号 %c -> %c", code, got)
		}
	}
}

func TestSecretFontReferenceRejectsDifferentBoundaries(t *testing.T) {
	outline := secretOutline{{{0, 0}, {400, 0}, {400, 400}, {0, 400}}}
	rotated := secretOutline{{{400, 400}, {400, 0}, {0, 0}, {0, 400}}}
	if !secretOutlinesNear(outline, rotated) {
		t.Fatal("起点和方向改变不应影响同一轮廓")
	}
	withNotch := secretOutline{{{0, 0}, {400, 0}, {400, 400}, {0, 400}, {0, 240}, {100, 200}, {0, 160}}}
	if secretOutlinesNear(outline, withNotch) {
		t.Fatal("包围盒相同但笔画不同的字形不能命中")
	}
	if secretOutlinesNear(outline, secretOutline{outline[0], outline[0]}) {
		t.Fatal("重复轮廓不能复用同一条笔画")
	}
}

func TestSecretFontReferenceRejectsDistinctUnicodeAliases(t *testing.T) {
	ref := secretReference()
	if ref == nil {
		t.Fatal("缺少内置轮廓库")
	}
	var entry secretReferenceEntry
	for _, candidate := range ref.entries {
		if candidate.code == '龘' {
			entry = candidate
			break
		}
	}
	if entry.gid == 0 {
		t.Fatal("缺少罕用字参考")
	}
	copyEntry := entry
	copyEntry.code = 'A'
	key := secretReferenceKey{entry.contours, secretReferenceBoundsKey(entry.bounds)}
	table := &secretFontReference{font: ref.font, entries: []secretReferenceEntry{entry, copyEntry}, buckets: map[secretReferenceKey][]int{key: {0, 1}}}
	result := table.match(ref.font, entry.gid)
	if result.found || !result.ambiguous {
		t.Fatal("两个不同字符共用轮廓时必须保留歧义")
	}
}

func TestSecretFontReferenceConcurrentDecoding(t *testing.T) {
	payload, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			decoder, err := newSecretFontDecoder(payload)
			if err != nil {
				t.Error(err)
				return
			}
			decoder.index = map[string]rune{}
			got, err := decoder.decodeText("崤崥崦崧崨")
			if err != nil || got != "指的性目含" {
				t.Errorf("并发解码不一致: %q/%v", got, err)
			}
		})
	}
	group.Wait()
}

func TestSecretFontReferenceCacheIsBounded(t *testing.T) {
	ref := secretReference()
	if ref == nil {
		t.Fatal("缺少参考库")
	}
	table := &secretFontReference{font: ref.font, entries: ref.entries, buckets: ref.buckets}
	table.cache.values = map[string]secretReferenceMatch{}
	for index := range maxSecretReferenceCache {
		key := fmt.Sprint(index)
		table.cache.keys = append(table.cache.keys, key)
		table.cache.values[key] = secretReferenceMatch{}
	}
	gid, err := ref.font.GlyphIndex(nil, '龘')
	if err != nil || gid == 0 {
		t.Fatal("参考字体缺少罕用字")
	}
	result := table.matchCached(ref.font, uint16(gid), "新签名")
	if !result.found || result.glyph != '龘' {
		t.Fatal("缓存替换没有正常解码")
	}
	if len(table.cache.values) != maxSecretReferenceCache || len(table.cache.keys) != maxSecretReferenceCache {
		t.Fatal("共享缓存超过容量")
	}
	if _, ok := table.cache.values["0"]; ok {
		t.Fatal("没有淘汰旧缓存")
	}
}

func TestSecretFontReferenceIgnoresUnusedInvalidGlyph(t *testing.T) {
	font := testSecretReferenceFont(t, map[rune]rune{0xe101: '龘'})
	cmap := testSecretFontTable(font, "cmap")
	// 额外组指向不存在的 GID；实际题面没有使用该码点。
	binary.BigEndian.PutUint32(cmap[12+12:], 2)
	binary.BigEndian.PutUint32(cmap[12+4:], 40)
	cmap = append(cmap, make([]byte, 12)...)
	binary.BigEndian.PutUint32(cmap[40:], 0xe102)
	binary.BigEndian.PutUint32(cmap[44:], 0xe102)
	binary.BigEndian.PutUint32(cmap[48:], 65535)
	font = testReplaceSecretFontTable(font, "cmap", cmap)
	decoder, err := newSecretFontDecoder(inlineSecretFontPayload(nil, font))
	if err != nil {
		t.Fatal(err)
	}
	decoder.index = map[string]rune{}
	if got, err := decoder.decodeText("\ue101"); err != nil || got != "龘" {
		t.Fatalf("未使用字形阻断题面: %q/%v", got, err)
	}
	if _, err := decoder.decodeText("\ue102"); err == nil {
		t.Fatal("实际使用的损坏字形必须失败")
	}
	if _, err := decoder.decodeText("\ue103"); err == nil {
		t.Fatal("字体未覆盖的私用码点必须失败")
	}
}

func TestSecretFontRewritePreservesPlainTextOutsideFontScope(t *testing.T) {
	payload, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.Replace(payload, []byte("</body>"), []byte(`<p id="plain">崤</p><script>\ue999</script></body>`), 1)
	decoder, err := newSecretFontDecoder(payload)
	if err != nil {
		t.Fatal(err)
	}
	root := parseHTMLDocument(payload)
	if err := rewriteSecretText(root, decoder); err != nil {
		t.Fatal(err)
	}
	plain := root.first(func(node *htmlNode) bool { return node.attr("id") == "plain" })
	if plain == nil || htmlNodeText(plain) != "崤" {
		t.Fatal("无关明文被字体映射改写")
	}
}

func TestSecretFontInlineWOFFAndUnicodeCmap(t *testing.T) {
	font := testSecretReferenceFont(t, map[rune]rune{0xe101: '龘'})
	cmap := testSecretFontTable(font, "cmap")
	binary.BigEndian.PutUint16(cmap[4:], 0)
	binary.BigEndian.PutUint16(cmap[6:], 4)
	font = testReplaceSecretFontTable(font, "cmap", cmap)
	woff := testSecretFontWOFF(font)
	decoder, err := newSecretFontDecoder(inlineSecretFontPayload(nil, woff))
	if err != nil {
		t.Fatal(err)
	}
	decoder.index = map[string]rune{}
	if got, err := decoder.decodeText("\ue101"); err != nil || got != "龘" {
		t.Fatalf("内联 WOFF/Unicode cmap 解码失败: %q/%v", got, err)
	}
}

func TestSecretFontReferenceArtifactIsValidAndReproducible(t *testing.T) {
	ref := secretReference()
	if ref == nil || len(ref.entries) < 40000 {
		t.Fatal("完整字符参考未载入")
	}
	if len(ref.data) > 24<<20 {
		t.Fatalf("参考数据超出内存预算: %d", len(ref.data))
	}
	fontLength := int(binary.LittleEndian.Uint32(ref.data[4:8]))
	result, err := BuildSecretFontReference(ref.data[12 : 12+fontLength])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, secretReferenceData) {
		t.Fatal("相同原版字体生成结果不一致")
	}
	broken := bytes.Clone(secretReferenceData)
	broken[len(broken)-5] ^= 1
	if _, err := parseSecretFontReference(broken); err == nil {
		t.Fatal("损坏参考库未被拒绝")
	}
}

// 用参考字体中的罕用字轮廓构造小型 TrueType 字体，重新分配随机码点。
// 夹具无需系统字体，不增加额外字体文件，也不复用被测解码结果作为标签。
func testSecretReferenceFont(t *testing.T, mappings map[rune]rune) []byte {
	t.Helper()
	ref := secretReference()
	if ref == nil {
		t.Fatal("缺少原版字体参考")
	}
	codes := make([]rune, 0, len(mappings))
	for code := range mappings {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	n := len(codes) + 1
	head, hhea, maxp := make([]byte, 54), make([]byte, 36), make([]byte, 32)
	binary.BigEndian.PutUint32(head, 0x00010000)
	binary.BigEndian.PutUint32(head[12:], 0x5f0f3cf5)
	binary.BigEndian.PutUint16(head[18:], 4000)
	binary.BigEndian.PutUint16(head[50:], 1)
	binary.BigEndian.PutUint32(hhea, 0x00010000)
	binary.BigEndian.PutUint16(hhea[34:], uint16(n))
	binary.BigEndian.PutUint32(maxp, 0x00010000)
	binary.BigEndian.PutUint16(maxp[4:], uint16(n))
	loca, hmtx := make([]byte, (n+1)*4), make([]byte, n*4)
	for i := range n {
		binary.BigEndian.PutUint16(hmtx[i*4:], 4000)
	}
	cmap := make([]byte, 28+len(codes)*12)
	binary.BigEndian.PutUint16(cmap[2:], 1)
	binary.BigEndian.PutUint16(cmap[4:], 3)
	binary.BigEndian.PutUint16(cmap[6:], 10)
	binary.BigEndian.PutUint32(cmap[8:], 12)
	binary.BigEndian.PutUint16(cmap[12:], 12)
	binary.BigEndian.PutUint32(cmap[16:], uint32(len(cmap)-12))
	binary.BigEndian.PutUint32(cmap[24:], uint32(len(codes)))
	var glyf bytes.Buffer
	for index, code := range codes {
		gid, err := ref.font.GlyphIndex(nil, mappings[code])
		if err != nil || gid == 0 {
			t.Fatalf("原版字体缺少 %U", mappings[code])
		}
		outline, err := secretReferenceOutline(ref.font, uint16(gid))
		if err != nil {
			t.Fatal(err)
		}
		bbox := secretOutlineBox(outline)
		binary.Write(&glyf, binary.BigEndian, int16(len(outline)))
		for _, value := range []int16{bbox.minX, -bbox.maxY, bbox.maxX, -bbox.minY} {
			binary.Write(&glyf, binary.BigEndian, value)
		}
		var points []secretOutlinePoint
		for _, contour := range outline {
			points = append(points, contour...)
			binary.Write(&glyf, binary.BigEndian, uint16(len(points)-1))
		}
		binary.Write(&glyf, binary.BigEndian, uint16(0))
		for range points {
			glyf.WriteByte(1)
		}
		last := int16(0)
		for _, p := range points {
			binary.Write(&glyf, binary.BigEndian, p.x-last)
			last = p.x
		}
		last = 0
		for _, p := range points {
			y := -p.y
			binary.Write(&glyf, binary.BigEndian, y-last)
			last = y
		}
		binary.BigEndian.PutUint32(loca[(index+2)*4:], uint32(glyf.Len()))
		at := 28 + index*12
		binary.BigEndian.PutUint32(cmap[at:], uint32(code))
		binary.BigEndian.PutUint32(cmap[at+4:], uint32(code))
		binary.BigEndian.PutUint32(cmap[at+8:], uint32(index+1))
	}
	post := make([]byte, 32)
	binary.BigEndian.PutUint32(post, 0x00030000)
	name := make([]byte, 6)
	binary.BigEndian.PutUint16(name[4:], 6)
	return testBuildSecretSFNT(map[string][]byte{"head": head, "hhea": hhea, "maxp": maxp, "loca": loca, "hmtx": hmtx, "glyf": glyf.Bytes(), "cmap": cmap, "post": post, "name": name})
}

func testBuildSecretSFNT(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	data := make([]byte, 12+16*len(tags))
	binary.BigEndian.PutUint32(data, 0x00010000)
	binary.BigEndian.PutUint16(data[4:], uint16(len(tags)))
	for index, tag := range tags {
		position := 12 + index*16
		copy(data[position:], tag)
		binary.BigEndian.PutUint32(data[position+8:], uint32(len(data)))
		binary.BigEndian.PutUint32(data[position+12:], uint32(len(tables[tag])))
		data = append(data, tables[tag]...)
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
	}
	return data
}

func testSecretFontTable(data []byte, tag string) []byte {
	for index := range int(binary.BigEndian.Uint16(data[4:6])) {
		position := 12 + index*16
		if string(data[position:position+4]) == tag {
			start := int(binary.BigEndian.Uint32(data[position+8:]))
			length := int(binary.BigEndian.Uint32(data[position+12:]))
			return bytes.Clone(data[start : start+length])
		}
	}
	return nil
}

func testReplaceSecretFontTable(data []byte, tag string, value []byte) []byte {
	tables := map[string][]byte{}
	for index := range int(binary.BigEndian.Uint16(data[4:6])) {
		position := 12 + index*16
		key := string(data[position : position+4])
		tables[key] = testSecretFontTable(data, key)
	}
	tables[tag] = value
	return testBuildSecretSFNT(tables)
}

func testSecretFontWOFF(data []byte) []byte {
	n := int(binary.BigEndian.Uint16(data[4:6]))
	result := make([]byte, 44+20*n)
	copy(result, "wOFF")
	copy(result[4:], data[:4])
	binary.BigEndian.PutUint16(result[12:], uint16(n))
	binary.BigEndian.PutUint32(result[16:], uint32(len(data)))
	for index := range n {
		position := 12 + 16*index
		tag := string(data[position : position+4])
		table := testSecretFontTable(data, tag)
		at := 44 + 20*index
		copy(result[at:], tag)
		binary.BigEndian.PutUint32(result[at+4:], uint32(len(result)))
		binary.BigEndian.PutUint32(result[at+8:], uint32(len(table)))
		binary.BigEndian.PutUint32(result[at+12:], uint32(len(table)))
		result = append(result, table...)
		for len(result)%4 != 0 {
			result = append(result, 0)
		}
	}
	binary.BigEndian.PutUint32(result[8:], uint32(len(result)))
	return result
}

func BenchmarkSecretFontReferenceWarmDecode(b *testing.B) {
	payload, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		b.Fatal(err)
	}
	decoder, err := newSecretFontDecoder(payload)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := decoder.decodeText("崤崥崦崧崨"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		decoder.cache = map[rune]rune{}
		if got, err := decoder.decodeText("崤崥崦崧崨"); err != nil || !strings.HasPrefix(got, "指") {
			b.Fatal(err)
		}
	}
}
