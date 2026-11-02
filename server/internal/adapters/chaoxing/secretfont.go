package chaoxing

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"sync"
)

// 学习通把未作答测验的题干渲染成随机码点，再用内嵌字体把每个码点画成真实字形。
// 码点每页随机，字形本身来自固定字体，因此按字形轮廓签名即可还原原文。
// 这里只做“签名查表”：没有命中就明确失败，不做猜测性映射。

// secretFontIndexData 保存字形签名到字符的映射，每行 “签名<TAB>字符”。
//
//go:embed secretfont_index.txt
var secretFontIndexData string

var (
	secretFontIndexOnce   sync.Once
	secretFontIndexTable  map[string]rune
	secretFontStyleRegexp = regexp.MustCompile(`(?is)<style[^>]*id\s*=\s*["']cxsecretstyle["'][^>]*>(.*?)</style>`)
	secretFontBase64      = regexp.MustCompile(`base64,([A-Za-z0-9+/=\s]+)`)
	secretFontFaceURL     = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")]+)['"]?\s*\)`)
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
	index      map[string]rune
	cache      map[rune]rune
	font       *secretFont
	features   *secretFeatureTable
}

func newSecretFontDecoder(payload []byte) (*secretFontDecoder, error) {
	index := secretFontIndex()
	if len(index) == 0 {
		return nil, errors.New("字形索引不可用")
	}
	font, err := pageSecretFont(payload)
	if err != nil {
		return nil, err
	}
	signatures, err := SecretFontSignatures(payload)
	if err != nil {
		return nil, err
	}
	return &secretFontDecoder{signatures: signatures, index: index, cache: map[rune]rune{},
		font: font, features: secretFeatures()}, nil
}

// resolve 返回码点对应的真实字符；未收录时返回 false，调用方必须终止解码。
func (d *secretFontDecoder) resolve(code rune) (rune, bool) {
	if value, found := d.cache[code]; found {
		return value, true
	}
	signature, used := d.signatures[code]
	if !used {
		return 0, false
	}
	if value, found := d.index[signature]; found {
		d.cache[code] = value
		return value, true
	}
	// 精确索引没收录时按形状兜底；置信度不足仍然失败，不做猜测。
	if matched := d.matchFeature(code); matched.ok {
		d.cache[code] = matched.glyph
		return matched.glyph, true
	}
	return 0, false
}

// decodeText 逐字替换；出现无法确定的码点时返回错误，不做猜测。
func (d *secretFontDecoder) decodeText(text string) (string, error) {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, code := range text {
		value, found := d.resolve(code)
		if !found {
			if _, used := d.signatures[code]; !used {
				builder.WriteRune(code)
				continue
			}
			return "", errors.New("字形未收录")
		}
		builder.WriteRune(value)
	}
	return builder.String(), nil
}

// secretFontBytes 从样式块取出内嵌字体；只接受内联 base64，不请求外部地址。
func secretFontBytes(payload []byte) ([]byte, error) {
	style := secretFontStyleRegexp.FindSubmatch(payload)
	if len(style) != 2 {
		return nil, errors.New("页面没有内嵌字体样式")
	}
	face := secretFontFaceURL.FindSubmatch(style[1])
	if len(face) != 2 {
		return nil, errors.New("内嵌字体缺少声明")
	}
	encoded := secretFontBase64.FindSubmatch(face[1])
	if len(encoded) != 2 {
		return nil, errors.New("内嵌字体不是内联数据")
	}
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, string(encoded[1]))
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil || len(decoded) < 12 {
		return nil, errors.New("内嵌字体数据无效")
	}
	return decoded, nil
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
	glyf, ok := tables["glyf"]
	if !ok {
		return nil, errors.New("字体缺少 glyf")
	}
	cmapTable, ok := tables["cmap"]
	if !ok {
		return nil, errors.New("字体缺少 cmap")
	}
	cmap, err := parseSecretFontCmap(data, cmapTable[0])
	if err != nil {
		return nil, err
	}
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
		if platform != 3 || (encoding != 1 && encoding != 10) {
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
		if end < start || end-start > 0x10000 {
			continue
		}
		for code := start; code <= end; code++ {
			value := gid + (code - start)
			if value > 0xFFFF {
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
	if depth > 6 {
		return nil, errors.New("复合字形层数过多")
	}
	if int(gid)+1 >= len(f.loca) {
		return nil, errors.New("字形编号越界")
	}
	start, end := f.loca[gid], f.loca[gid+1]
	if end <= start || int(end) > len(f.glyf) {
		return nil, errors.New("字形数据缺失")
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
	root.walk(func(node *htmlNode) bool {
		if failure != nil {
			return false
		}
		if node.tag == "#text" && node.text != "" {
			replaced, err := decoder.decodeText(node.text)
			if err != nil {
				failure = err
				return false
			}
			node.text = replaced
			return true
		}
		// 选项文字优先取 aria-label，属性同样带着加密文本，必须一起还原。
		for _, key := range []string{"aria-label", "title"} {
			value, found := node.attrs[key]
			if !found || value == "" {
				continue
			}
			replaced, err := decoder.decodeText(value)
			if err != nil {
				failure = err
				return false
			}
			node.attrs[key] = replaced
		}
		return true
	})
	return failure
}

// secretFeatureMargin 是兜底匹配的最小置信差距，低于它一律转人工。
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
	if !result.found {
		return secretFeatureMatch{glyph: result.glyph, margin: result.margin}
	}
	return secretFeatureMatch{glyph: result.glyph, margin: result.margin, ok: true}
}
