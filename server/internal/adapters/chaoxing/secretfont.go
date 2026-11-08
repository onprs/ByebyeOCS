package chaoxing

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font/sfnt"
)

// 学习通把未作答测验的题干渲染成随机码点，再用内嵌字体把每个码点画成真实字形。
// 码点每页随机，字形保留原版轮廓，可与源字体完整 Unicode 目录核对还原。
// 原版完整 Unicode 字体提供几何参考；旧签名索引只补充已核对的别名。
// 仅采用绝对轮廓误差内的唯一结果，不按低分辨率相似度猜字。

// secretFontIndexData 保存字形签名到字符的映射，每行 “签名<TAB>字符”。
//
//go:embed secretfont_index.txt
var secretFontIndexData string

var (
	secretFontIndexOnce  sync.Once
	secretFontIndexTable map[string]rune
	// 平台不同课程使用过 cxSecretStyle 及其变体；按包含匹配兼容后缀，避免依赖单一 id。
	secretFontStyleRegexp    = regexp.MustCompile(`(?is)<style[^>]*id\s*=\s*["'][^"']*cxsecretstyle[^"']*["'][^>]*>(.*?)</style>`)
	secretFontAnyStyleRegexp = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	secretFontFacePattern    = regexp.MustCompile(`(?i)@font-face`)
	secretFontBase64         = regexp.MustCompile(`base64,([A-Za-z0-9+/=\s]+)`)
	secretFontFaceURL        = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")]+)['"]?\s*\)`)
)

// secretFontIndex 返回签名索引；索引文件缺失或格式错误时返回空表，解码随之失败。
func secretFontIndex() map[string]rune {
	secretFontIndexOnce.Do(func() {
		table := map[string]rune{}
		scanner := bufio.NewScanner(strings.NewReader(secretFontIndexData))
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			signature, glyph, found := strings.Cut(line, "\t")
			runes := []rune(strings.TrimSpace(glyph))
			if !found || len(signature) != secretFontSignatureLength || len(runes) != 1 {
				continue
			}
			table[signature] = runes[0]
		}
		secretFontIndexTable = table
	})
	return secretFontIndexTable
}

const secretFontSignatureLength = 32

type secretGlyphPoint struct {
	x, y    int32
	onCurve bool
}

type secretFont struct {
	unitsPerEm int
	loca       []uint32
	glyf       []byte
	cmap       map[rune]uint16
	// CFF/CFF2 字体没有 glyf 表，轮廓由标准解析器提供。
	sfnt *sfnt.Font
}

// SecretFontSignatures 返回页面内嵌字体每个码点的轮廓签名，供维护工具构建索引。
func SecretFontSignatures(payload []byte) (map[rune]string, error) {
	font, err := pageSecretFont(payload)
	if err != nil {
		return nil, err
	}
	result := make(map[rune]string, len(font.cmap))
	for code, gid := range font.cmap {
		signature, err := font.signature(gid)
		if err != nil {
			return nil, err
		}
		result[code] = signature
	}
	if len(result) == 0 {
		return nil, errors.New("字体没有可解码字形")
	}
	return result, nil
}

// glyphSignatures 返回码点签名，并单独标记没有任何轮廓的空白字形。
// 空白字形在页面上本就不可见，按空格处理；有轮廓却无法识别的字形仍然失败。
func (f *secretFont) glyphSignatures() (map[rune]string, map[rune]bool, error) {
	signatures := make(map[rune]string, len(f.cmap))
	empty := make(map[rune]bool)
	for code, gid := range f.cmap {
		signature, err := f.signature(gid)
		if err != nil {
			if isEmptySecretGlyph(err) {
				empty[code] = true
				continue
			}
			return nil, nil, err
		}
		signatures[code] = signature
	}
	if len(signatures) == 0 && len(empty) == 0 {
		return nil, nil, errors.New("字体没有可解码字形")
	}
	return signatures, empty, nil
}

// isEmptySecretGlyph 只把明确的空轮廓视为空白；其他错误继续失败。
func isEmptySecretGlyph(err error) bool {
	if err == nil {
		return false
	}
	detail := err.Error()
	return detail == "字形轮廓为空"
}

func pageSecretFont(payload []byte) (*secretFont, error) {
	raw, err := secretFontBytes(payload)
	if err != nil {
		return nil, err
	}
	return parseSecretFont(raw)
}

// secretFontDecoder 只在真正遇到码点时解析，字体里未被使用的字形不会阻断页面。
type secretFontDecoder struct {
	signatures map[rune]string
	empty      map[rune]bool
	index      map[string]rune
	cache      map[rune]rune
	font       *secretFont
	outline    *sfnt.Font
	reference  *secretFontReference
	lastReason string
	// 旧特征匹配仅供维护诊断，正式解码不加载或采用其最近邻结果。
	features   *secretFeatureTable
	lastMargin float64
}

func newSecretFontDecoder(payload []byte) (*secretFontDecoder, error) {
	raw, err := secretFontBytes(payload)
	if err != nil {
		return nil, err
	}
	font, err := parseSecretFont(raw)
	if err != nil {
		return nil, err
	}
	if len(font.cmap) == 0 {
		return nil, errors.New("字体没有可解码字形")
	}
	outline, _ := sfnt.Parse(raw)
	reference := secretReference()
	index := secretFontIndex()
	if reference == nil && len(index) == 0 {
		return nil, errors.New("字形索引不可用")
	}
	return &secretFontDecoder{signatures: map[rune]string{}, empty: map[rune]bool{}, index: index,
		cache: map[rune]rune{}, font: font, outline: outline, reference: reference}, nil
}

type secretGlyphStatus int

const (
	glyphMissing secretGlyphStatus = iota
	glyphEmpty
	glyphResolved
)

// resolve 返回码点对应的真实字符；空白字形单独标记，其余未收录情况终止解码。
func (d *secretFontDecoder) resolve(code rune) (rune, secretGlyphStatus) {
	if value, found := d.cache[code]; found {
		return value, glyphResolved
	}
	if d.empty[code] {
		return ' ', glyphEmpty
	}
	gid, used := d.font.cmap[code]
	if !used {
		return 0, glyphMissing
	}
	signature, parsed := d.signatures[code]
	if !parsed {
		var err error
		signature, err = d.font.signature(gid)
		if err != nil {
			if isEmptySecretGlyph(err) {
				d.empty[code] = true
				return ' ', glyphEmpty
			}
			d.lastReason = "字形轮廓无法读取"
			return 0, glyphMissing
		}
		d.signatures[code] = signature
	}
	d.lastReason = "缺少匹配轮廓"
	if d.reference != nil {
		key := strconv.Itoa(d.font.unitsPerEm) + ":" + signature
		matched := d.reference.matchCached(d.outline, gid, key)
		if matched.found {
			d.cache[code] = matched.glyph
			return matched.glyph, glyphResolved
		}
		if matched.ambiguous {
			d.lastReason = "字形存在别名歧义"
		}
	}
	// 少量汉字与其他 Unicode 符号共享视觉轮廓，保留既有已核对的精确签名。
	if value, found := d.index[signature]; found {
		d.cache[code] = value
		return value, glyphResolved
	}
	return 0, glyphMissing
}

// decodeText 逐字替换；出现无法确定的码点时返回错误，不做猜测。
func (d *secretFontDecoder) decodeText(text string) (string, error) {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, code := range text {
		value, status := d.resolve(code)
		switch status {
		case glyphResolved:
			builder.WriteRune(value)
		case glyphEmpty:
			// 平台字体用空字形表示空白字符，保留为空格与页面视觉一致。
			builder.WriteRune(' ')
		default:
			if _, used := d.font.cmap[code]; !used && !isPrivateUseRune(code) {
				builder.WriteRune(code)
				continue
			}
			return "", fmt.Errorf("字形未收录（%s）", d.lastReason)
		}
	}
	return builder.String(), nil
}

// secretFontBytes 从样式块取出内嵌字体；只接受内联 base64，不请求外部地址。
// 优先匹配 cxSecretStyle 及其变体；没有该 id 时回退到含 @font-face 与内联字体的 style。
func secretFontBytes(payload []byte) ([]byte, error) {
	style := secretFontStyleBlock(payload)
	if len(style) == 0 {
		return nil, errors.New("页面没有内嵌字体样式")
	}
	for _, face := range secretFontFaceURL.FindAllSubmatch(style, maxSecretFontCandidates) {
		if len(face) != 2 {
			continue
		}
		decoded, ok := decodeSecretFontDataURL(string(face[1]))
		if !ok {
			continue
		}
		if normalized := normalizeSecretFontData(decoded); normalized != nil {
			return normalized, nil
		}
	}
	return nil, errors.New("内嵌字体数据无效或格式未支持")
}

func parseSecretFont(data []byte) (*secretFont, error) {
	if len(data) < 12 {
		return nil, errors.New("字体长度不足")
	}
	tables := map[string][2]uint32{}
	count := int(binary.BigEndian.Uint16(data[4:6]))
	for index := 0; index < count; index++ {
		offset := 12 + index*16
		if offset+16 > len(data) {
			return nil, errors.New("字体表目录越界")
		}
		tag := string(data[offset : offset+4])
		start := binary.BigEndian.Uint32(data[offset+8 : offset+12])
		length := binary.BigEndian.Uint32(data[offset+12 : offset+16])
		if uint64(start)+uint64(length) > uint64(len(data)) {
			return nil, errors.New("字体表长度越界")
		}
		tables[tag] = [2]uint32{start, length}
	}
	head, ok := tables["head"]
	if !ok || head[1] < 54 {
		return nil, errors.New("字体缺少 head")
	}
	unitsPerEm := int(binary.BigEndian.Uint16(data[head[0]+18 : head[0]+20]))
	cmapTable, ok := tables["cmap"]
	if !ok {
		return nil, errors.New("字体缺少 cmap")
	}
	cmap, err := parseSecretFontCmap(data, cmapTable[0])
	if err != nil {
		return nil, err
	}
	// CFF/CFF2 字体没有 glyf 表，改用标准解析器提取轮廓；cmap 仍用同一份解析结果。
	if _, hasGlyf := tables["glyf"]; !hasGlyf {
		if _, cff1 := tables["CFF "]; !cff1 {
			if _, cff2 := tables["CFF2"]; !cff2 {
				return nil, errors.New("字体缺少可解析字形")
			}
		}
		parsed, err := sfnt.Parse(data)
		if err != nil {
			return nil, errors.New("CFF 字体结构无法解析")
		}
		return &secretFont{unitsPerEm: unitsPerEm, cmap: cmap, sfnt: parsed}, nil
	}
	indexToLoc := int16(binary.BigEndian.Uint16(data[head[0]+50 : head[0]+52]))
	maxp, ok := tables["maxp"]
	if !ok || maxp[1] < 6 {
		return nil, errors.New("字体缺少 maxp")
	}
	glyphCount := int(binary.BigEndian.Uint16(data[maxp[0]+4 : maxp[0]+6]))
	loca, ok := tables["loca"]
	if !ok {
		return nil, errors.New("字体缺少 loca")
	}
	offsets := make([]uint32, glyphCount+1)
	for index := 0; index <= glyphCount; index++ {
		if indexToLoc == 0 {
			position := int(loca[0]) + index*2
			if position+2 > len(data) {
				return nil, errors.New("loca 越界")
			}
			offsets[index] = uint32(binary.BigEndian.Uint16(data[position:position+2])) * 2
			continue
		}
		position := int(loca[0]) + index*4
		if position+4 > len(data) {
			return nil, errors.New("loca 越界")
		}
		offsets[index] = binary.BigEndian.Uint32(data[position : position+4])
	}
	glyf := tables["glyf"]
	return &secretFont{unitsPerEm: unitsPerEm, loca: offsets,
		glyf: data[glyf[0] : glyf[0]+glyf[1]], cmap: cmap}, nil
}

func parseSecretFontCmap(data []byte, start uint32) (map[rune]uint16, error) {
	base := int(start)
	if base+4 > len(data) {
		return nil, errors.New("cmap 越界")
	}
	count := int(binary.BigEndian.Uint16(data[base+2 : base+4]))
	result := map[rune]uint16{}
	for index := 0; index < count; index++ {
		position := base + 4 + index*8
		if position+8 > len(data) {
			return nil, errors.New("cmap 目录越界")
		}
		platform := binary.BigEndian.Uint16(data[position : position+2])
		encoding := binary.BigEndian.Uint16(data[position+2 : position+4])
		offset := base + int(binary.BigEndian.Uint32(data[position+4:position+8]))
		if platform != 0 && (platform != 3 || (encoding != 1 && encoding != 10)) {
			continue
		}
		if offset+2 > len(data) {
			continue
		}
		switch binary.BigEndian.Uint16(data[offset : offset+2]) {
		case 4:
			if err := parseSecretFontCmap4(data, offset, result); err != nil {
				return nil, err
			}
		case 12:
			if err := parseSecretFontCmap12(data, offset, result); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func parseSecretFontCmap4(data []byte, offset int, target map[rune]uint16) error {
	if offset+14 > len(data) {
		return errors.New("cmap4 越界")
	}
	segmentsX2 := int(binary.BigEndian.Uint16(data[offset+6 : offset+8]))
	segments := segmentsX2 / 2
	endsAt := offset + 14
	startsAt := endsAt + segmentsX2 + 2
	deltasAt := startsAt + segmentsX2
	rangesAt := deltasAt + segmentsX2
	if rangesAt+segmentsX2 > len(data) {
		return errors.New("cmap4 段越界")
	}
	for index := 0; index < segments; index++ {
		end := binary.BigEndian.Uint16(data[endsAt+index*2 : endsAt+index*2+2])
		begin := binary.BigEndian.Uint16(data[startsAt+index*2 : startsAt+index*2+2])
		if begin == 0xFFFF {
			continue
		}
		delta := binary.BigEndian.Uint16(data[deltasAt+index*2 : deltasAt+index*2+2])
		rangeOffset := binary.BigEndian.Uint16(data[rangesAt+index*2 : rangesAt+index*2+2])
		for code := int(begin); code <= int(end); code++ {
			var gid uint16
			if rangeOffset == 0 {
				gid = uint16(code) + delta
			} else {
				position := rangesAt + index*2 + int(rangeOffset) + (code-int(begin))*2
				if position+2 > len(data) {
					continue
				}
				gid = binary.BigEndian.Uint16(data[position : position+2])
				if gid != 0 {
					gid += delta
				}
			}
			if gid != 0 {
				target[rune(code)] = gid
			}
		}
	}
	return nil
}

func parseSecretFontCmap12(data []byte, offset int, target map[rune]uint16) error {
	if offset+16 > len(data) {
		return errors.New("cmap12 越界")
	}
	groups := int(binary.BigEndian.Uint32(data[offset+12 : offset+16]))
	for index := 0; index < groups; index++ {
		position := offset + 16 + index*12
		if position+12 > len(data) {
			return errors.New("cmap12 组越界")
		}
		start := binary.BigEndian.Uint32(data[position : position+4])
		end := binary.BigEndian.Uint32(data[position+4 : position+8])
		gid := binary.BigEndian.Uint32(data[position+8 : position+12])
		if end < start || end > utf8.MaxRune || end-start > 0x10000 {
			continue
		}
		for code := start; code <= end; code++ {
			value := gid + (code - start)
			if value == 0 || value > 0xFFFF || !utf8.ValidRune(rune(code)) {
				continue
			}
			target[rune(code)] = uint16(value)
		}
	}
	return nil
}

// signature 计算字形轮廓签名：绝对坐标点、闭合轮廓与端点只保留几何信息，
// 不含字体内部编号、指令或填充细节，因此同一字形在不同子集中签名一致。
func (f *secretFont) signature(gid uint16) (string, error) {
	contours, err := f.contours(gid, 0)
	if err != nil {
		return "", err
	}
	if len(contours) == 0 {
		return "", errors.New("字形轮廓为空")
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 1024))
	var header [4]byte
	binary.BigEndian.PutUint16(header[0:2], uint16(len(contours)))
	buffer.Write(header[:2])
	for _, contour := range contours {
		binary.BigEndian.PutUint16(header[0:2], uint16(len(contour)))
		buffer.Write(header[:2])
		for _, point := range contour {
			binary.BigEndian.PutUint32(header[0:4], uint32(int32(point.x)))
			buffer.Write(header[:4])
			binary.BigEndian.PutUint32(header[0:4], uint32(int32(point.y)))
			buffer.Write(header[:4])
			if point.onCurve {
				buffer.WriteByte(1)
				continue
			}
			buffer.WriteByte(0)
		}
	}
	sum := sha256.Sum256(buffer.Bytes())
	return hex.EncodeToString(sum[:])[:secretFontSignatureLength], nil
}

func (f *secretFont) contours(gid uint16, depth int) ([][]secretGlyphPoint, error) {
	if f.sfnt != nil {
		return f.sfntContours(gid)
	}
	if depth > 6 {
		return nil, errors.New("复合字形层数过多")
	}
	if int(gid)+1 >= len(f.loca) {
		return nil, errors.New("字形编号越界")
	}
	start, end := f.loca[gid], f.loca[gid+1]
	if end < start || int(end) > len(f.glyf) {
		return nil, fmt.Errorf("字形数据越界(gid=%d start=%d end=%d glyf=%d loca=%d)",
			gid, start, end, len(f.glyf), len(f.loca))
	}
	if end == start {
		return nil, nil
	}
	data := f.glyf[start:end]
	if len(data) < 10 {
		return nil, errors.New("字形头长度不足")
	}
	contourCount := int16(binary.BigEndian.Uint16(data[0:2]))
	if contourCount < 0 {
		return f.compositeContours(data, depth)
	}
	if contourCount == 0 {
		return nil, nil
	}
	count := int(contourCount)
	ends := make([]uint16, count)
	for index := 0; index < count; index++ {
		position := 10 + index*2
		if position+2 > len(data) {
			return nil, errors.New("轮廓端点越界")
		}
		ends[index] = binary.BigEndian.Uint16(data[position : position+2])
	}
	points := int(ends[count-1]) + 1
	position := 10 + count*2
	if position+2 > len(data) {
		return nil, errors.New("字形指令长度缺失")
	}
	instructions := int(binary.BigEndian.Uint16(data[position : position+2]))
	position += 2 + instructions
	flags := make([]byte, 0, points)
	for len(flags) < points {
		if position >= len(data) {
			return nil, errors.New("字形标记越界")
		}
		flag := data[position]
		position++
		flags = append(flags, flag)
		if flag&0x08 != 0 {
			if position >= len(data) {
				return nil, errors.New("字形重复标记越界")
			}
			repeat := int(data[position])
			position++
			for index := 0; index < repeat && len(flags) < points; index++ {
				flags = append(flags, flag)
			}
		}
	}
	xs := make([]int32, points)
	var value int32
	for index, flag := range flags {
		switch {
		case flag&0x02 != 0:
			if position >= len(data) {
				return nil, errors.New("字形 X 坐标越界")
			}
			delta := int32(data[position])
			position++
			if flag&0x10 == 0 {
				delta = -delta
			}
			value += delta
		case flag&0x10 == 0:
			if position+2 > len(data) {
				return nil, errors.New("字形 X 坐标越界")
			}
			value += int32(int16(binary.BigEndian.Uint16(data[position : position+2])))
			position += 2
		}
		xs[index] = value
	}
	ys := make([]int32, points)
	value = 0
	for index, flag := range flags {
		switch {
		case flag&0x04 != 0:
			if position >= len(data) {
				return nil, errors.New("字形 Y 坐标越界")
			}
			delta := int32(data[position])
			position++
			if flag&0x20 == 0 {
				delta = -delta
			}
			value += delta
		case flag&0x20 == 0:
			if position+2 > len(data) {
				return nil, errors.New("字形 Y 坐标越界")
			}
			value += int32(int16(binary.BigEndian.Uint16(data[position : position+2])))
			position += 2
		}
		ys[index] = value
	}
	contours := make([][]secretGlyphPoint, 0, count)
	startIndex := 0
	for _, end := range ends {
		limit := int(end)
		if limit < startIndex || limit >= points {
			return nil, errors.New("轮廓端点顺序无效")
		}
		contour := make([]secretGlyphPoint, 0, limit-startIndex+1)
		for index := startIndex; index <= limit; index++ {
			contour = append(contour, secretGlyphPoint{x: xs[index], y: ys[index], onCurve: flags[index]&0x01 != 0})
		}
		contours = append(contours, contour)
		startIndex = limit + 1
	}
	return contours, nil
}

func (f *secretFont) compositeContours(data []byte, depth int) ([][]secretGlyphPoint, error) {
	position := 10
	var result [][]secretGlyphPoint
	for position+4 <= len(data) {
		flags := binary.BigEndian.Uint16(data[position : position+2])
		component := binary.BigEndian.Uint16(data[position+2 : position+4])
		position += 4
		var dx, dy int32
		if flags&0x0001 != 0 {
			if position+4 > len(data) {
				return nil, errors.New("复合字形偏移越界")
			}
			dx = int32(int16(binary.BigEndian.Uint16(data[position : position+2])))
			dy = int32(int16(binary.BigEndian.Uint16(data[position+2 : position+4])))
			position += 4
		} else {
			if position+2 > len(data) {
				return nil, errors.New("复合字形偏移越界")
			}
			dx = int32(int8(data[position]))
			dy = int32(int8(data[position+1]))
			position += 2
		}
		scale := [4]int32{0x4000, 0, 0, 0x4000}
		switch {
		case flags&0x0008 != 0:
			if position+2 > len(data) {
				return nil, errors.New("复合字形缩放越界")
			}
			value := int32(int16(binary.BigEndian.Uint16(data[position : position+2])))
			scale = [4]int32{value, 0, 0, value}
			position += 2
		case flags&0x0040 != 0:
			if position+4 > len(data) {
				return nil, errors.New("复合字形缩放越界")
			}
			scale = [4]int32{int32(int16(binary.BigEndian.Uint16(data[position : position+2]))), 0, 0,
				int32(int16(binary.BigEndian.Uint16(data[position+2 : position+4])))}
			position += 4
		case flags&0x0080 != 0:
			if position+8 > len(data) {
				return nil, errors.New("复合字形变换越界")
			}
			scale = [4]int32{
				int32(int16(binary.BigEndian.Uint16(data[position : position+2]))),
				int32(int16(binary.BigEndian.Uint16(data[position+2 : position+4]))),
				int32(int16(binary.BigEndian.Uint16(data[position+4 : position+6]))),
				int32(int16(binary.BigEndian.Uint16(data[position+6 : position+8]))),
			}
			position += 8
		}
		componentContours, err := f.contours(component, depth+1)
		if err != nil {
			return nil, err
		}
		for _, contour := range componentContours {
			transformed := make([]secretGlyphPoint, 0, len(contour))
			for _, point := range contour {
				x := int32((int64(point.x)*int64(scale[0]) + int64(point.y)*int64(scale[2])) / 0x4000)
				y := int32((int64(point.x)*int64(scale[1]) + int64(point.y)*int64(scale[3])) / 0x4000)
				transformed = append(transformed, secretGlyphPoint{x: x + dx, y: y + dy, onCurve: point.onCurve})
			}
			result = append(result, transformed)
		}
		if flags&0x0020 == 0 {
			break
		}
	}
	return result, nil
}

// rewriteSecretText 把文本节点里的随机码点替换成真实字符。
func rewriteSecretText(root *htmlNode, decoder *secretFontDecoder) error {
	if root == nil || decoder == nil {
		return nil
	}
	var failure error
	explicitScope := root.firstByClass("font-cxsecret") != nil
	// 脚本与样式里的模板文本不是题目内容，字体子集也不保证覆盖，
	// 不能因为其中的码点缺失而阻断整页解码。
	var visit func(*htmlNode, bool) bool
	visit = func(node *htmlNode, encrypted bool) bool {
		if failure != nil || node == nil {
			return false
		}
		switch node.tag {
		case "script", "style", "noscript", "template":
			return true
		}
		encrypted = encrypted || node.hasClass("font-cxsecret")
		if encrypted && node.tag == "#text" && node.text != "" {
			replaced, err := decoder.decodeText(node.text)
			if err != nil {
				failure = err
				return false
			}
			node.text = replaced
		}
		// 选项文字优先取 aria-label，属性同样带着加密文本，必须一起还原。
		for _, key := range []string{"aria-label", "title"} {
			value, found := node.attrs[key]
			if !encrypted || !found || value == "" {
				continue
			}
			replaced, err := decoder.decodeText(value)
			if err != nil {
				failure = err
				return false
			}
			node.attrs[key] = replaced
		}
		for _, child := range node.children {
			if !visit(child, encrypted) {
				return false
			}
		}
		return true
	}
	visit(root, !explicitScope)
	return failure
}

// secretFeatureMargin 只用于历史特征方案的维护对照，不决定正式解码结果。
const secretFeatureMargin = 0.7

type secretFeatureMatch struct {
	glyph  rune
	margin float64
	ok     bool
}

func (d *secretFontDecoder) matchFeature(code rune) secretFeatureMatch {
	if d.font == nil || d.features == nil {
		return secretFeatureMatch{}
	}
	gid, found := d.font.cmap[code]
	if !found {
		return secretFeatureMatch{}
	}
	feature, ok := secretGlyphFeature(d.font, gid)
	if !ok {
		return secretFeatureMatch{}
	}
	result := d.features.match(feature, secretFeatureMargin)
	d.lastMargin = result.margin
	if !result.found {
		return secretFeatureMatch{glyph: result.glyph, margin: result.margin}
	}
	return secretFeatureMatch{glyph: result.glyph, margin: result.margin, ok: true}
}

// secretFontStyleBlock 返回内联字体所在的样式块内容。
// 先按 cxSecretStyle 变体匹配，再回退到任意包含 @font-face 和内联 base64 的 style；
// 仅返回样式块，不解析或记录题目内容。
func secretFontStyleBlock(payload []byte) []byte {
	if match := secretFontStyleRegexp.FindSubmatch(payload); len(match) == 2 {
		return match[1]
	}
	for _, match := range secretFontAnyStyleRegexp.FindAllSubmatch(payload, -1) {
		if len(match) != 2 || !secretFontFacePattern.Match(match[1]) {
			continue
		}
		if secretFontBase64.Match(match[1]) {
			return match[1]
		}
	}
	return nil
}

const (
	maxSecretFontStyleBytes = 1 << 20
	maxSecretFontFileBytes  = 4 << 20
	maxSecretFontLinks      = 8
	maxSecretFontCandidates = 12
)

var (
	secretFontLinkPattern   = regexp.MustCompile(`(?is)<link[^>]*>`)
	secretFontLinkRel       = regexp.MustCompile(`(?is)rel\s*=\s*["']([^"']*)["']`)
	secretFontLinkHref      = regexp.MustCompile(`(?is)href\s*=\s*["']([^"']+)["']`)
	secretFontFaceBlock     = regexp.MustCompile(`(?is)@font-face\s*\{[^}]*\}`)
	secretFontFaceSourceURL = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")]+)['"]?\s*\)`)
	secretFontImportPattern = regexp.MustCompile(`(?is)@import\s+(?:url\(\s*)?['"]?([^\s'";)]+)['"]?\s*\)?[^;]*;`)
	secretFontHeadPattern   = regexp.MustCompile(`(?is)<head(?:\s[^>]*)?>`)
)

// inlineSecretFontPayload 把外部字体以内联样式追加到页面，复用同一套字形解析。
func inlineSecretFontPayload(payload, font []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(font)
	kind := "TTF"
	if len(font) >= 4 && string(font[:4]) == "OTTO" {
		kind = "OTTO"
	}
	style := `<!--secretfont:` + kind + `-->` +
		`<style id="cxSecretStyle">@font-face{src:url('data:font/ttf;base64,` + encoded + `')}</style>`
	result := make([]byte, 0, len(payload)+len(style))
	at := 0
	if head := secretFontHeadPattern.FindIndex(payload); head != nil {
		at = head[1]
	}
	result = append(result, payload[:at]...)
	result = append(result, style...)
	result = append(result, payload[at:]...)
	return result
}

// fetchSecretFontCandidates 收集同域样式表里的所有 @font-face 字体。
// 页面通常同时引用界面字体和加密字体，必须逐个尝试，不能假定第一个就是密文字体。
// 只访问平台允许的 HTTPS 地址；静态资源请求使用无 Cookie 客户端，避免把会话带出平台。
func (p *Platform) fetchSecretFontCandidates(ctx context.Context, payload []byte, pageURL *url.URL) [][]byte {
	if pageURL == nil {
		return nil
	}
	type sheet struct {
		base *url.URL
		css  []byte
	}
	type face struct {
		base  *url.URL
		block []byte
	}
	var queue []sheet
	var faces []face
	seenStyles := map[string]bool{}
	load := func(base *url.URL, raw string) {
		target, ok := p.secretFontAssetURL(base, raw)
		if !ok || seenStyles[target.String()] || len(seenStyles) >= maxSecretFontLinks {
			return
		}
		seenStyles[target.String()] = true
		css, err := p.fetchSecretFontAsset(ctx, target.String(), maxSecretFontStyleBytes, "text/css,*/*;q=0.8")
		if err == nil {
			queue = append(queue, sheet{target, css})
		}
	}
	// 内联样式也可能引用外部字体或 @import；基准地址随样式表逐级变化。
	for _, style := range secretFontAnyStyleRegexp.FindAllSubmatch(payload, maxSecretFontLinks) {
		if len(style) == 2 {
			queue = append(queue, sheet{pageURL, style[1]})
		}
	}
	for _, link := range secretFontLinkPattern.FindAll(payload, maxSecretFontLinks) {
		rel, href := secretFontLinkRel.FindSubmatch(link), secretFontLinkHref.FindSubmatch(link)
		if len(rel) == 2 && len(href) == 2 && strings.Contains(strings.ToLower(string(rel[1])), "stylesheet") {
			load(pageURL, string(href[1]))
		}
	}
	for index := 0; index < len(queue); index++ {
		current := queue[index]
		for _, block := range secretFontFaceBlock.FindAll(current.css, maxSecretFontCandidates) {
			faces = append(faces, face{current.base, block})
		}
		for _, imported := range secretFontImportPattern.FindAllSubmatch(current.css, maxSecretFontLinks) {
			if len(imported) == 2 {
				load(current.base, string(imported[1]))
			}
		}
	}
	// 平台标记的加密字体优先；其他候选仍需实际改写题面码点后才会采用。
	sort.SliceStable(faces, func(i, j int) bool {
		left := bytes.Contains(bytes.ToLower(faces[i].block), []byte("cxsecret"))
		right := bytes.Contains(bytes.ToLower(faces[j].block), []byte("cxsecret"))
		return left && !right
	})
	candidates := make([][]byte, 0, 4)
	seenFonts := map[[32]byte]bool{}
	for index, item := range faces {
		if index >= maxSecretFontCandidates {
			break
		}
		font := p.resolveSecretFontSource(ctx, item.base, item.block)
		if len(font) < 12 {
			continue
		}
		sum := sha256.Sum256(font)
		if !seenFonts[sum] {
			seenFonts[sum] = true
			candidates = append(candidates, font)
		}
	}
	return candidates
}

// resolveSecretFontSource 解析单个 @font-face：优先内联 data URL，其次同域字体文件。
func (p *Platform) resolveSecretFontSource(ctx context.Context, base *url.URL, block []byte) []byte {
	for _, match := range secretFontFaceSourceURL.FindAllSubmatch(block, maxSecretFontCandidates) {
		if len(match) != 2 {
			continue
		}
		source := strings.TrimSpace(string(match[1]))
		if strings.HasPrefix(strings.ToLower(source), "data:") {
			if data, ok := decodeSecretFontDataURL(source); ok {
				if normalized := normalizeSecretFontData(data); normalized != nil {
					return normalized
				}
			}
			continue
		}
		target, ok := p.secretFontAssetURL(base, source)
		if !ok {
			continue
		}
		data, err := p.fetchSecretFontAsset(ctx, target.String(), maxSecretFontFileBytes, "font/ttf,font/otf,font/woff,application/octet-stream;q=0.8")
		if err == nil {
			if normalized := normalizeSecretFontData(data); normalized != nil {
				return normalized
			}
		}
	}
	return nil
}

// secretFontAssetURL 解析相对地址，仅接受平台允许的 HTTPS 地址。
func (p *Platform) secretFontAssetURL(base *url.URL, raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return nil, false
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	if !target.IsAbs() {
		target = base.ResolveReference(target)
	}
	if target.Scheme == "http" && target.Port() == "" && p.factory.allowedHost(target.Hostname()) {
		target.Scheme = "https"
	}
	if !p.factory.allowedURL(target) {
		return nil, false
	}
	return target, true
}

// fetchSecretFontAsset 读取样式或字体静态资源，使用独立无 Cookie 客户端并限制体积。
func (p *Platform) fetchSecretFontAsset(ctx context.Context, rawURL string, max int64, accept string) ([]byte, error) {
	target, ok := p.secretFontAssetURL(&url.URL{Scheme: "https", Host: "mooc1.chaoxing.com"}, rawURL)
	if !ok {
		return nil, errors.New("字体资源地址不在允许范围")
	}
	if err := p.factory.limiter.wait(ctx, p.requestInterval(), p.requestIntervalFloor()); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", coverUserAgent)
	request.Header.Set("Accept", accept)
	request.Header.Set("Referer", "https://mooc2-ans.chaoxing.com/")
	response, err := p.coverClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("字体资源响应未成功")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || int64(len(payload)) > max {
		return nil, errors.New("字体资源超出允许体积")
	}
	return payload, nil
}

// decodeSecretFontDataURL 解码内联字体，不接受其他媒体类型。
func decodeSecretFontDataURL(source string) ([]byte, bool) {
	index := strings.Index(strings.ToLower(source), "base64,")
	if index < 0 {
		return nil, false
	}
	encoded := source[index+len("base64,"):]
	encoded = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, encoded)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if data, err := encoding.DecodeString(encoded); err == nil && len(data) >= 12 {
			return data, true
		}
	}
	return nil, false
}

// normalizeSecretFontData 接受 sfnt 或 WOFF，统一转换为可解析的 sfnt 字体。
func normalizeSecretFontData(data []byte) []byte {
	if isSFNTFont(data) {
		return data
	}
	if len(data) >= 4 && string(data[:4]) == "wOFF" {
		if converted, err := woffToSFNT(data); err == nil {
			return converted
		}
	}
	return nil
}

func isSFNTFont(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	switch {
	case data[0] == 0 && data[1] == 1 && data[2] == 0 && data[3] == 0:
		return true
	case string(data[:4]) == "true", string(data[:4]) == "OTTO":
		return true
	}
	return false
}

// woffToSFNT 解包 WOFF1 为 sfnt；只处理字形解析需要的表，保持原有表内容。
func woffToSFNT(data []byte) ([]byte, error) {
	if len(data) < 44 || string(data[:4]) != "wOFF" {
		return nil, errors.New("不是 WOFF 字体")
	}
	flavor := binary.BigEndian.Uint32(data[4:8])
	numTables := int(binary.BigEndian.Uint16(data[12:14]))
	if numTables <= 0 || numTables > 512 || len(data) < 44+numTables*20 {
		return nil, errors.New("WOFF 表目录无效")
	}
	type entry struct {
		tag, checksum uint32
		offset        int
		length        int
	}
	entries := make([]entry, 0, numTables)
	total := 12 + numTables*16
	for i := 0; i < numTables; i++ {
		base := 44 + i*20
		tag := binary.BigEndian.Uint32(data[base : base+4])
		offset := int(binary.BigEndian.Uint32(data[base+4 : base+8]))
		compLength := int(binary.BigEndian.Uint32(data[base+8 : base+12]))
		origLength := int(binary.BigEndian.Uint32(data[base+12 : base+16]))
		checksum := binary.BigEndian.Uint32(data[base+16 : base+20])
		if offset < 0 || compLength < 0 || origLength < 0 || offset+compLength > len(data) ||
			origLength > maxSecretFontFileBytes {
			return nil, errors.New("WOFF 表长度无效")
		}
		entries = append(entries, entry{tag: tag, checksum: checksum, offset: offset, length: origLength})
		total += (origLength + 3) &^ 3
	}
	result := make([]byte, total)
	binary.BigEndian.PutUint32(result[0:4], flavor)
	binary.BigEndian.PutUint16(result[4:6], uint16(numTables))
	// 搜索参数按 sfnt 规范计算，部分解析器会读取。
	power := 1
	for power*2 <= numTables {
		power *= 2
	}
	binary.BigEndian.PutUint16(result[6:8], uint16(power*16))
	entrySelector := 0
	for (1 << (entrySelector + 1)) <= power {
		entrySelector++
	}
	binary.BigEndian.PutUint16(result[8:10], uint16(entrySelector))
	binary.BigEndian.PutUint16(result[10:12], uint16(numTables*16-power*16))
	cursor := 12 + numTables*16
	for i, item := range entries {
		base := 44 + i*20
		compLength := int(binary.BigEndian.Uint32(data[base+8 : base+12]))
		origLength := int(binary.BigEndian.Uint32(data[base+12 : base+16]))
		raw := data[item.offset : item.offset+compLength]
		var table []byte
		if compLength < origLength {
			reader, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, err
			}
			table, err = io.ReadAll(io.LimitReader(reader, int64(origLength)))
			reader.Close()
			if err != nil || len(table) != origLength {
				return nil, errors.New("WOFF 表解压失败")
			}
		} else {
			table = raw
		}
		header := 12 + i*16
		binary.BigEndian.PutUint32(result[header:header+4], item.tag)
		binary.BigEndian.PutUint32(result[header+4:header+8], item.checksum)
		binary.BigEndian.PutUint32(result[header+8:header+12], uint32(cursor))
		binary.BigEndian.PutUint32(result[header+12:header+16], uint32(item.length))
		copy(result[cursor:cursor+len(table)], table)
		cursor += (item.length + 3) &^ 3
	}
	return result, nil
}

// ensureSecretFont 在页面只引用外部样式字体时，取同域字体并内联，供统一解码使用。
// 页面可能同时加载多个字体，逐个试解码，只有能完整还原文本的字体才会采用。
func (p *Platform) ensureSecretFont(ctx context.Context, payload []byte, pageURL *url.URL) ([]byte, bool) {
	hasInline := secretFontStyleBlock(payload) != nil
	if hasInline {
		decoder, err := newSecretFontDecoder(payload)
		if err == nil && rewriteSecretText(parseHTMLDocument(payload), decoder) == nil && decoder.hasRemappedText() {
			return payload, true
		}
	}
	if !hasInline && !workPageHasEncryptedFont(payload) {
		return payload, false
	}
	for _, font := range p.fetchSecretFontCandidates(ctx, payload, pageURL) {
		candidate := inlineSecretFontPayload(payload, font)
		decoder, err := newSecretFontDecoder(candidate)
		if err != nil {
			continue
		}
		if err = rewriteSecretText(parseHTMLDocument(candidate), decoder); err != nil || !decoder.hasRemappedText() {
			continue
		}
		return candidate, true
	}
	// 明确下发的内联字体仍交给解析器给出具体错误，不能静默放过加密内容。
	return payload, hasInline
}

func (d *secretFontDecoder) hasRemappedText() bool {
	for code, value := range d.cache {
		if code != value {
			return true
		}
	}
	for code := range d.empty {
		if !unicode.IsSpace(code) {
			return true
		}
	}
	return false
}
