package chaoxing

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"unicode/utf8"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

// 参考轮廓直接取自原版字体的 Unicode cmap；平台重新编号、反转轮廓或转换曲线
// 都不改变字的几何边界。以千分之一 em 为坐标，四分之一单位为边界取样精度。
const (
	secretReferenceScale     = 4
	secretReferenceFlatness  = 0.2
	secretReferenceTolerance = 6.0
	secretReferenceBucket    = 8 * secretReferenceScale
	maxSecretReferenceBytes  = 32 << 20
)

type secretOutlinePoint struct{ x, y int16 }
type secretOutline [][]secretOutlinePoint
type secretOutlineBounds struct{ minX, minY, maxX, maxY int16 }

type secretReferenceKey struct {
	contours uint16
	bounds   secretOutlineBounds
}

type secretReferenceEntry struct {
	code          rune
	gid, contours uint16
	bounds        secretOutlineBounds
}

type secretFontReference struct {
	data    []byte
	font    *sfnt.Font
	entries []secretReferenceEntry
	buckets map[secretReferenceKey][]int
	cache   secretReferenceCache
}

type secretReferenceMatch struct {
	glyph     rune
	ambiguous bool
	found     bool
}

// normalizeSecretReferenceRune 只归并 Unicode 明确定义的汉字兼容字与部首别名。
// 其他符号、大小写、全角和半角保留，不能因长得相似而合并不同字符。
func normalizeSecretReferenceRune(code rune) rune {
	if equivalent, ok := secretReferenceUnicodeEquivalents[code]; ok {
		return equivalent
	}
	if code >= 0x3190 && code <= 0x319f || code >= 0x2e80 && code <= 0x2fdf || code >= 0xf900 && code <= 0xfaff || code >= 0x2f800 && code <= 0x2fa1f {
		value := []rune(norm.NFKC.String(string(code)))
		if len(value) == 1 {
			return value[0]
		}
	}
	return code
}

// secretReferenceOutline 按真实曲线的误差上限自适应展开，保持位置、比例和笔画。
// 不依赖字形编号、hint 指令、轮廓起点、顺序、方向或字形包围盒元数据。
func secretReferenceOutline(font *sfnt.Font, gid uint16) (secretOutline, error) {
	if font == nil {
		return nil, errors.New("缺少标准字形解析器")
	}
	var buffer sfnt.Buffer
	segments, err := font.LoadGlyph(&buffer, sfnt.GlyphIndex(gid), fixed.I(1000), nil)
	if err != nil {
		return nil, errors.New("参考轮廓无法读取")
	}
	var outline secretOutline
	var contour []secretOutlinePoint
	var current [2]float64
	var failure error
	emit := func(p [2]float64) {
		x, y := math.Round(p[0]*secretReferenceScale), math.Round(p[1]*secretReferenceScale)
		if x < math.MinInt16 || x > math.MaxInt16 || y < math.MinInt16 || y > math.MaxInt16 {
			failure = errors.New("参考轮廓坐标越界")
			return
		}
		point := secretOutlinePoint{int16(x), int16(y)}
		if len(contour) == 0 || contour[len(contour)-1] != point {
			contour = append(contour, point)
		}
	}
	flush := func() {
		if len(contour) > 1 && contour[0] == contour[len(contour)-1] {
			contour = contour[:len(contour)-1]
		}
		if len(contour) >= 3 {
			outline = append(outline, contour)
		}
		contour = nil
	}
	point := func(p fixed.Point26_6) [2]float64 { return [2]float64{float64(p.X) / 64, float64(p.Y) / 64} }
	mid := func(a, b [2]float64) [2]float64 { return [2]float64{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2} }
	var quad func([2]float64, [2]float64, [2]float64, int)
	quad = func(a, b, c [2]float64, depth int) {
		if depth >= 12 || secretLineDistance(b, a, c) <= secretReferenceFlatness {
			emit(c)
			return
		}
		ab, bc := mid(a, b), mid(b, c)
		m := mid(ab, bc)
		quad(a, ab, m, depth+1)
		quad(m, bc, c, depth+1)
	}
	var cubic func([2]float64, [2]float64, [2]float64, [2]float64, int)
	cubic = func(a, b, c, d [2]float64, depth int) {
		if depth >= 12 || math.Max(secretLineDistance(b, a, d), secretLineDistance(c, a, d)) <= secretReferenceFlatness {
			emit(d)
			return
		}
		ab, bc, cd := mid(a, b), mid(b, c), mid(c, d)
		abc, bcd := mid(ab, bc), mid(bc, cd)
		m := mid(abc, bcd)
		cubic(a, ab, abc, m, depth+1)
		cubic(m, bcd, cd, d, depth+1)
	}
	for _, segment := range segments {
		switch segment.Op {
		case sfnt.SegmentOpMoveTo:
			flush()
			current = point(segment.Args[0])
			emit(current)
		case sfnt.SegmentOpLineTo:
			current = point(segment.Args[0])
			emit(current)
		case sfnt.SegmentOpQuadTo:
			end := point(segment.Args[1])
			quad(current, point(segment.Args[0]), end, 0)
			current = end
		case sfnt.SegmentOpCubeTo:
			end := point(segment.Args[2])
			cubic(current, point(segment.Args[0]), point(segment.Args[1]), end, 0)
			current = end
		}
		if failure != nil {
			return nil, failure
		}
		if len(contour) > 16384 || len(outline) > 1024 {
			return nil, errors.New("参考轮廓复杂度超限")
		}
	}
	flush()
	return outline, nil
}

func secretLineDistance(p, a, b [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	if dx == 0 && dy == 0 {
		return math.Hypot(p[0]-a[0], p[1]-a[1])
	}
	t := math.Max(0, math.Min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dy)/(dx*dx+dy*dy)))
	return math.Hypot(p[0]-a[0]-t*dx, p[1]-a[1]-t*dy)
}

func secretContourBounds(contour []secretOutlinePoint) secretOutlineBounds {
	b := secretOutlineBounds{math.MaxInt16, math.MaxInt16, math.MinInt16, math.MinInt16}
	for _, p := range contour {
		b.minX = min(b.minX, p.x)
		b.minY = min(b.minY, p.y)
		b.maxX = max(b.maxX, p.x)
		b.maxY = max(b.maxY, p.y)
	}
	return b
}

func secretOutlineBox(outline secretOutline) secretOutlineBounds {
	b := secretOutlineBounds{math.MaxInt16, math.MaxInt16, math.MinInt16, math.MinInt16}
	for _, contour := range outline {
		c := secretContourBounds(contour)
		b.minX = min(b.minX, c.minX)
		b.minY = min(b.minY, c.minY)
		b.maxX = max(b.maxX, c.maxX)
		b.maxY = max(b.maxY, c.maxY)
	}
	return b
}

func secretBoundsNear(a, b secretOutlineBounds) bool {
	limit := int(secretReferenceTolerance * secretReferenceScale)
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	return abs(int(a.minX)-int(b.minX)) <= limit && abs(int(a.minY)-int(b.minY)) <= limit && abs(int(a.maxX)-int(b.maxX)) <= limit && abs(int(a.maxY)-int(b.maxY)) <= limit
}

func secretReferenceBin(value int16) int16 {
	// 负坐标也按向下取整分桶，保证边界查询对称。
	return int16(math.Floor(float64(value) / secretReferenceBucket))
}

func secretReferenceBoundsKey(b secretOutlineBounds) secretOutlineBounds {
	return secretOutlineBounds{secretReferenceBin(b.minX), secretReferenceBin(b.minY), secretReferenceBin(b.maxX), secretReferenceBin(b.maxY)}
}

// BuildSecretFontReference 遍历源字体自身的完整 cmap，自动生成可复现的几何参考。
// 不读取任何课程、题目或人工字形标签，构建过程与业务执行完全独立。
func BuildSecretFontReference(fontBytes []byte) ([]byte, error) {
	font, err := sfnt.Parse(fontBytes)
	if err != nil {
		return nil, errors.New("源字体无法解析")
	}
	parsed, err := parseSecretFont(fontBytes)
	if err != nil {
		return nil, err
	}
	codes := make([]rune, 0, len(parsed.cmap))
	aliases := map[uint16]map[rune]bool{}
	for code, gid := range parsed.cmap {
		if !utf8.ValidRune(code) || gid == 0 || isPrivateUseRune(code) {
			continue
		}
		codes = append(codes, code)
		if aliases[gid] == nil {
			aliases[gid] = map[rune]bool{}
		}
		aliases[gid][normalizeSecretReferenceRune(code)] = true
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	var result bytes.Buffer
	result.WriteString("SFR2")
	binary.Write(&result, binary.LittleEndian, uint32(len(fontBytes)))
	binary.Write(&result, binary.LittleEndian, uint32(0))
	result.Write(fontBytes)
	count := uint32(0)
	seen := map[[2]uint32]bool{}
	for _, code := range codes {
		gid := parsed.cmap[code]
		canonical := secretReferenceCanonicalAlias(normalizeSecretReferenceRune(code), aliases[gid])
		key := [2]uint32{uint32(canonical), uint32(gid)}
		if seen[key] {
			continue
		}
		seen[key] = true
		outline, err := secretReferenceOutline(font, gid)
		if err != nil {
			return nil, fmt.Errorf("源字体 %U: %w", code, err)
		}
		if len(outline) == 0 {
			continue
		}
		bounds := secretOutlineBox(outline)
		binary.Write(&result, binary.LittleEndian, uint32(canonical))
		binary.Write(&result, binary.LittleEndian, gid)
		binary.Write(&result, binary.LittleEndian, uint16(len(outline)))
		for _, value := range []int16{bounds.minX, bounds.minY, bounds.maxX, bounds.maxY} {
			binary.Write(&result, binary.LittleEndian, value)
		}
		count++
	}
	if count == 0 || result.Len() > maxSecretReferenceBytes {
		return nil, errors.New("参考字体为空或超出体积限制")
	}
	binary.LittleEndian.PutUint32(result.Bytes()[8:], count)
	var compressed bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if _, err = writer.Write(result.Bytes()); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

// 同一个源 GID 具有注音/注记与唯一汉字别名时采用汉字；只对原版 cmap
// 明确共享的字形生效，几何相似的两个汉字仍保留为独立候选。
func secretReferenceCanonicalAlias(code rune, aliases map[rune]bool) rune {
	var han rune
	for alias := range aliases {
		if isSecretUnifiedHan(alias) {
			if han != 0 && han != alias {
				return code
			}
			han = alias
		} else if !(alias >= 0x3100 && alias <= 0x312f || alias >= 0x3190 && alias <= 0x319f) {
			return code
		}
	}
	if han != 0 {
		return han
	}
	return code
}

func isSecretUnifiedHan(code rune) bool {
	return code >= 0x3400 && code <= 0x4dbf || code >= 0x4e00 && code <= 0x9fff || code >= 0x20000 && code <= 0x323af
}

func isPrivateUseRune(code rune) bool {
	return code >= 0xe000 && code <= 0xf8ff || code >= 0xf0000 && code <= 0xffffd || code >= 0x100000 && code <= 0x10fffd
}

func parseSecretFontReference(compressed []byte) (*secretFontReference, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, errors.New("参考轮廓库压缩格式无效")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxSecretReferenceBytes+1))
	if err != nil || len(data) > maxSecretReferenceBytes {
		return nil, errors.New("参考轮廓库损坏或体积超限")
	}
	if len(data) < 12 || string(data[:4]) != "SFR2" {
		return nil, errors.New("参考轮廓库格式无效")
	}
	fontLength := int(binary.LittleEndian.Uint32(data[4:8]))
	count := int(binary.LittleEndian.Uint32(data[8:12]))
	if fontLength > len(data)-12 || count <= 0 || count > 0x110000 || len(data)-12-fontLength != count*16 {
		return nil, errors.New("参考轮廓库长度无效")
	}
	font, err := sfnt.Parse(data[12 : 12+fontLength])
	if err != nil {
		return nil, errors.New("参考字体结构无效")
	}
	table := &secretFontReference{data: data, font: font, entries: make([]secretReferenceEntry, 0, count), buckets: make(map[secretReferenceKey][]int)}
	for position := 12 + fontLength; position < len(data); position += 16 {
		entry := secretReferenceEntry{
			code:     rune(binary.LittleEndian.Uint32(data[position:])),
			gid:      binary.LittleEndian.Uint16(data[position+4:]),
			contours: binary.LittleEndian.Uint16(data[position+6:]),
			bounds:   secretOutlineBounds{int16(binary.LittleEndian.Uint16(data[position+8:])), int16(binary.LittleEndian.Uint16(data[position+10:])), int16(binary.LittleEndian.Uint16(data[position+12:])), int16(binary.LittleEndian.Uint16(data[position+14:]))},
		}
		if !utf8.ValidRune(entry.code) || isPrivateUseRune(entry.code) || int(entry.gid) >= font.NumGlyphs() || entry.contours == 0 || entry.contours > 1024 || entry.bounds.minX > entry.bounds.maxX || entry.bounds.minY > entry.bounds.maxY {
			return nil, errors.New("参考轮廓条目无效")
		}
		key := secretReferenceKey{contours: entry.contours, bounds: secretReferenceBoundsKey(entry.bounds)}
		table.buckets[key] = append(table.buckets[key], len(table.entries))
		table.entries = append(table.entries, entry)
	}
	return table, nil
}

func (t *secretFontReference) outline(entry secretReferenceEntry) (secretOutline, bool) {
	outline, err := secretReferenceOutline(t.font, entry.gid)
	return outline, err == nil && len(outline) == int(entry.contours)
}

// secretContoursNear 双向核对整条边界，每条线段均匀取样，不受曲线分段影响。
// 单独的相似度差距无法证明正确，只有绝对几何误差内的唯一字符才允许采用。
func secretContoursNear(a, b []secretOutlinePoint) bool {
	near := func(source, target []secretOutlinePoint) bool {
		limit := secretReferenceTolerance * secretReferenceScale
		limitSquared := limit * limit
		for index, start := range source {
			end := source[(index+1)%len(source)]
			dx, dy := float64(end.x)-float64(start.x), float64(end.y)-float64(start.y)
			steps := max(1, int(math.Ceil(math.Hypot(dx, dy)/(8*secretReferenceScale))))
			for step := 0; step < steps; step++ {
				x, y := float64(start.x)+dx*float64(step)/float64(steps), float64(start.y)+dy*float64(step)/float64(steps)
				found := false
				for ti, p := range target {
					q := target[(ti+1)%len(target)]
					vx, vy := float64(q.x)-float64(p.x), float64(q.y)-float64(p.y)
					px, py := x-float64(p.x), y-float64(p.y)
					projection := 0.0
					if length := vx*vx + vy*vy; length > 0 {
						projection = math.Max(0, math.Min(1, (px*vx+py*vy)/length))
					}
					ex, ey := px-projection*vx, py-projection*vy
					if ex*ex+ey*ey <= limitSquared {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
		}
		return true
	}
	return near(a, b) && near(b, a)
}

func secretOutlinesNear(a, b secretOutline) bool {
	if len(a) != len(b) {
		return false
	}
	// 每个轮廓必须一对一匹配，避免重复笔画被同一个参考轮廓吸收。
	matches := make([]int, len(b))
	for i := range matches {
		matches[i] = -1
	}
	candidates := make([][]int, len(a))
	for i, ca := range a {
		ba := secretContourBounds(ca)
		for j, cb := range b {
			if secretBoundsNear(ba, secretContourBounds(cb)) && secretContoursNear(ca, cb) {
				candidates[i] = append(candidates[i], j)
			}
		}
		if len(candidates[i]) == 0 {
			return false
		}
	}
	var assign func(int, []bool) bool
	assign = func(i int, visited []bool) bool {
		for _, j := range candidates[i] {
			if visited[j] {
				continue
			}
			visited[j] = true
			if matches[j] < 0 || assign(matches[j], visited) {
				matches[j] = i
				return true
			}
		}
		return false
	}
	for i := range a {
		if !assign(i, make([]bool, len(b))) {
			return false
		}
	}
	return true
}

func (t *secretFontReference) match(font *sfnt.Font, gid uint16) secretReferenceMatch {
	outline, err := secretReferenceOutline(font, gid)
	if err != nil || len(outline) == 0 {
		return secretReferenceMatch{}
	}
	bounds := secretOutlineBox(outline)
	delta := int(secretReferenceTolerance * secretReferenceScale)
	expand := func(value int16, direction int) int16 {
		return int16(max(math.MinInt16, min(math.MaxInt16, int(value)+direction*delta)))
	}
	low := secretReferenceBoundsKey(secretOutlineBounds{expand(bounds.minX, -1), expand(bounds.minY, -1), expand(bounds.maxX, -1), expand(bounds.maxY, -1)})
	high := secretReferenceBoundsKey(secretOutlineBounds{expand(bounds.minX, 1), expand(bounds.minY, 1), expand(bounds.maxX, 1), expand(bounds.maxY, 1)})
	var result secretReferenceMatch
	for x1 := low.minX; x1 <= high.minX; x1++ {
		for y1 := low.minY; y1 <= high.minY; y1++ {
			for x2 := low.maxX; x2 <= high.maxX; x2++ {
				for y2 := low.maxY; y2 <= high.maxY; y2++ {
					key := secretReferenceKey{uint16(len(outline)), secretOutlineBounds{x1, y1, x2, y2}}
					for _, index := range t.buckets[key] {
						entry := t.entries[index]
						if !secretBoundsNear(bounds, entry.bounds) {
							continue
						}
						reference, ok := t.outline(entry)
						if !ok || !secretOutlinesNear(outline, reference) {
							continue
						}
						if result.found && result.glyph != entry.code {
							return secretReferenceMatch{ambiguous: true}
						}
						result = secretReferenceMatch{glyph: entry.code, found: true}
					}
				}
			}
		}
	}
	return result
}
