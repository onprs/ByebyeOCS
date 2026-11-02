package chaoxing

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// 字形特征用于“精确签名未收录”时的兜底匹配：把字形渲染到固定坐标系后，
// 盒式平均降采样并二值化，得到与字体内部表示无关的形状特征。
// 固定坐标系避免加密字体被改写过的 hhea/bbox 干扰：不裁墨迹、不依赖字体度量。
const (
	secretCanvasSize   = 480
	secretFontSize     = 360
	secretBaseline     = 384
	secretCoarseGrid   = 16
	secretFineGrid     = 24
	secretSamplesPerAx = 6
	// 二值化阈值偏低：细横笔在 30 像素格里只有 30%~50% 覆盖率，按 50% 会被吞掉。
	secretBinaryLevel  = 40
	secretCoarseSize   = secretCoarseGrid * secretCoarseGrid
	secretFineSize     = secretFineGrid * secretFineGrid
	secretFeatureBytes = secretCoarseSize + secretFineSize
	// 精特征保留灰度，避免细小笔画在二值化时消失（太/大 这类只差一个点）。
	secretShortlist = 40
)

// secretFeatureDebug 仅用于维护期诊断，正式运行保持 nil。
var secretFeatureDebug func([][]secretPoint, float64, float64, float64)

type secretFeature struct {
	value [secretFeatureBytes]byte
}

type secretPoint struct {
	x, y    float64
	onCurve bool
}

// secretGlyphFeature 按固定坐标系渲染字形并提取粗/精二值特征。
// 返回 false 表示字形没有可渲染轮廓（空白字或数据损坏）。
func secretGlyphFeature(font *secretFont, gid uint16) (secretFeature, bool) {
	contours, err := font.contours(gid, 0)
	if err != nil || len(contours) == 0 {
		return secretFeature{}, false
	}
	scale := float64(secretFontSize) / float64(font.unitsPerEm)
	polygons := make([][]secretPoint, 0, len(contours))
	minX, maxX := math.Inf(1), math.Inf(-1)
	for _, contour := range contours {
		count := len(contour)
		if count < 3 {
			continue
		}
		// 先补出相邻控制点之间的隐含中点，使控制点总是夹在两个端点之间。
		ring := make([]secretGlyphPoint, 0, count*2)
		for index := 0; index < count; index++ {
			current := contour[index]
			next := contour[(index+1)%count]
			ring = append(ring, current)
			if !current.onCurve && !next.onCurve {
				ring = append(ring, secretGlyphPoint{x: (current.x + next.x) / 2, y: (current.y + next.y) / 2, onCurve: true})
			}
		}
		// 旋转到以 on-curve 点开头。
		offset := 0
		for index, point := range ring {
			if point.onCurve {
				offset = index
				break
			}
		}
		polygon := make([]secretPoint, 0, len(ring)*2)
		emit := func(value secretGlyphPoint) {
			x, y := float64(value.x)*scale, float64(value.y)*scale
			polygon = append(polygon, secretPoint{x: x, y: y, onCurve: true})
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
		}
		var anchor secretGlyphPoint
		anchorSet := false
		for step := 0; step < len(ring); step++ {
			point := ring[(offset+step)%len(ring)]
			if point.onCurve {
				if !anchorSet {
					emit(point)
					anchor = point
					anchorSet = true
					continue
				}
				emit(point)
				anchor = point
				continue
			}
			follow := ring[(offset+step+1)%len(ring)]
			if !anchorSet {
				continue
			}
			sx, sy := float64(anchor.x)*scale, float64(anchor.y)*scale
			cx, cy := float64(point.x)*scale, float64(point.y)*scale
			ex, ey := float64(follow.x)*scale, float64(follow.y)*scale
			for sample := 1; sample < secretSamplesPerAx; sample++ {
				t := float64(sample) / float64(secretSamplesPerAx)
				x := (1-t)*(1-t)*sx + 2*(1-t)*t*cx + t*t*ex
				y := (1-t)*(1-t)*sy + 2*(1-t)*t*cy + t*t*ey
				polygon = append(polygon, secretPoint{x: x, y: y, onCurve: true})
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
			}
		}
		if len(polygon) >= 3 {
			polygons = append(polygons, polygon)
		}
	}
	if len(polygons) == 0 || math.IsInf(minX, 1) {
		return secretFeature{}, false
	}
	offsetX := (secretCanvasSize - (maxX - minX)) / 2
	if secretFeatureDebug != nil {
		secretFeatureDebug(polygons, minX, maxX, offsetX)
	}
	shifted := make([][]secretPoint, 0, len(polygons))
	for _, polygon := range polygons {
		points := make([]secretPoint, 0, len(polygon))
		for _, point := range polygon {
			points = append(points, secretPoint{x: point.x - minX + offsetX, y: secretBaseline - point.y, onCurve: true})
		}
		shifted = append(shifted, points)
	}
	feature := secretFeature{}
	coarse := coverageGrid(shifted, secretCoarseGrid)
	fine := coverageGrid(shifted, secretFineGrid)
	for index := 0; index < secretCoarseSize; index++ {
		if coarse[index] >= secretBinaryLevel {
			feature.value[index] = 1
		}
	}
	for index := 0; index < secretFineSize; index++ {
		if fine[index] >= secretBinaryLevel {
			feature.value[secretCoarseSize+index] = 1
		}
	}
	return feature, true
}

// coverageGrid 用等距采样估算每个格子的墨迹覆盖率（0-255）。
func coverageGrid(polygons [][]secretPoint, grid int) []byte {
	cell := float64(secretCanvasSize) / float64(grid)
	result := make([]byte, grid*grid)
	for row := 0; row < grid; row++ {
		for column := 0; column < grid; column++ {
			inside := 0
			for sy := 0; sy < secretSamplesPerAx; sy++ {
				for sx := 0; sx < secretSamplesPerAx; sx++ {
					x := (float64(column) + (float64(sx)+0.5)/float64(secretSamplesPerAx)) * cell
					y := (float64(row) + (float64(sy)+0.5)/float64(secretSamplesPerAx)) * cell
					if pointInPolygons(polygons, x, y) {
						inside++
					}
				}
			}
			result[row*grid+column] = byte(inside * 255 / (secretSamplesPerAx * secretSamplesPerAx))
		}
	}
	return result
}

// pointInPolygons 使用奇偶规则判断点是否在字形墨迹内。
func pointInPolygons(polygons [][]secretPoint, x, y float64) bool {
	inside := false
	for _, polygon := range polygons {
		count := len(polygon)
		for index := 0; index < count; index++ {
			current := polygon[index]
			next := polygon[(index+1)%count]
			if (current.y > y) == (next.y > y) {
				continue
			}
			intersection := (next.x-current.x)*(y-current.y)/(next.y-current.y) + current.x
			if x < intersection {
				inside = !inside
			}
		}
	}
	return inside
}

// secretFeatureTable 是离线生成的全量特征表，用于兜底匹配。
type secretFeatureTable struct {
	count      int
	coarseGrid int
	fineGrid   int
	codepoints []rune
	coarse     []byte
	fine       []byte
}

// parseSecretFeatureTable 解析 FRB2 二进制特征表。
func parseSecretFeatureTable(payload []byte) (*secretFeatureTable, error) {
	if len(payload) < 20 || string(payload[:4]) != "FRB2" {
		return nil, errors.New("特征表格式无效")
	}
	count := int(binary.LittleEndian.Uint32(payload[4:8]))
	coarseGrid := int(binary.LittleEndian.Uint32(payload[8:12]))
	fineGrid := int(binary.LittleEndian.Uint32(payload[12:16]))
	if count <= 0 || count > 1<<21 || coarseGrid < 4 || coarseGrid > 64 || fineGrid < 4 || fineGrid > 64 {
		return nil, errors.New("特征表头部无效")
	}
	headerEnd := 16
	if len(payload) < headerEnd+4 {
		return nil, errors.New("特征表缺少校验值")
	}
	headerEnd += 4
	coarseBytes := coarseGrid * coarseGrid / 8
	fineBytes := fineGrid * fineGrid
	if coarseGrid*coarseGrid%8 != 0 || fineGrid*fineGrid%8 != 0 {
		return nil, errors.New("特征表格子不能整除 8")
	}
	need := headerEnd + count*4 + count*coarseBytes + count*fineBytes
	if len(payload) < need {
		return nil, errors.New("特征表长度不足")
	}
	table := &secretFeatureTable{count: count, coarseGrid: coarseGrid, fineGrid: fineGrid,
		codepoints: make([]rune, count),
		coarse:     make([]byte, count*coarseGrid*coarseGrid),
		fine:       make([]byte, count*fineGrid*fineGrid)}
	position := headerEnd
	for index := 0; index < count; index++ {
		table.codepoints[index] = rune(binary.LittleEndian.Uint32(payload[position : position+4]))
		position += 4
	}
	unpackBits(payload[position:position+count*coarseBytes], table.coarse)
	position += count * coarseBytes
	copy(table.fine, payload[position:position+count*fineBytes])
	return table, nil
}

func unpackBits(source []byte, target []byte) {
	for index := 0; index < len(target); index++ {
		value := source[index/8] >> (7 - uint(index%8))
		target[index] = value & 1
	}
}

type secretMatch struct {
	glyph  rune
	margin float64
	found  bool
}

// match 用粗/精两级最近邻匹配；差距不足时明确返回未命中。
func (t *secretFeatureTable) match(feature secretFeature, minMargin float64) secretMatch {
	coarseSize := t.coarseGrid * t.coarseGrid
	fineSize := t.fineGrid * t.fineGrid
	if coarseSize != secretCoarseSize || fineSize != secretFineSize {
		return secretMatch{}
	}
	coarse := feature.value[:coarseSize]
	fine := feature.value[coarseSize:]
	type scored struct {
		index int
		score int
	}
	shortlist := make([]scored, 0, secretShortlist)
	for index := 0; index < t.count; index++ {
		score := 0
		base := index * coarseSize
		for bit := 0; bit < coarseSize; bit++ {
			if t.coarse[base+bit] != coarse[bit] {
				score++
			}
		}
		if len(shortlist) < secretShortlist {
			shortlist = append(shortlist, scored{index: index, score: score})
			if len(shortlist) == secretShortlist {
				sort.Slice(shortlist, func(i, j int) bool { return shortlist[i].score < shortlist[j].score })
			}
			continue
		}
		if score < shortlist[len(shortlist)-1].score {
			shortlist[len(shortlist)-1] = scored{index: index, score: score}
			sort.Slice(shortlist, func(i, j int) bool { return shortlist[i].score < shortlist[j].score })
		}
	}
	if len(shortlist) == 0 {
		return secretMatch{}
	}
	bestIndex, bestScore, secondScore := -1, math.MaxInt, math.MaxInt
	for _, item := range shortlist {
		score := 0
		base := item.index * fineSize
		for bit := 0; bit < fineSize; bit++ {
			delta := int(t.fine[base+bit]) - int(fine[bit])
			if delta < 0 {
				delta = -delta
			}
			score += delta
		}
		if score < bestScore {
			secondScore = bestScore
			bestScore = score
			bestIndex = item.index
		} else if score < secondScore {
			secondScore = score
		}
	}
	if bestIndex < 0 || secondScore == math.MaxInt || bestScore == 0 && secondScore == 0 {
		return secretMatch{}
	}
	margin := 1 - float64(bestScore)/float64(secondScore)
	if margin < minMargin {
		return secretMatch{glyph: t.codepoints[bestIndex], margin: margin}
	}
	return secretMatch{glyph: t.codepoints[bestIndex], margin: margin, found: true}
}

// BuildSecretFeatureTable 用给定字体与码点生成 FRB2 特征表，供维护工具离线构建。
// 运行时只读取生成结果，不在请求路径上生成特征表。
func BuildSecretFeatureTable(fontBytes []byte, codepoints []rune) ([]byte, error) {
	font, err := parseSecretFont(fontBytes)
	if err != nil {
		return nil, err
	}
	type entry struct {
		code    rune
		feature secretFeature
	}
	entries := make([]entry, 0, len(codepoints))
	for _, code := range codepoints {
		gid, found := font.cmap[code]
		if !found {
			continue
		}
		feature, ok := secretGlyphFeature(font, gid)
		if !ok {
			continue
		}
		entries = append(entries, entry{code: code, feature: feature})
	}
	if len(entries) == 0 {
		return nil, errors.New("没有可生成特征的字形")
	}
	coarseBytes := secretCoarseSize / 8
	fineBytes := secretFineSize
	size := 20 + len(entries)*4 + len(entries)*coarseBytes + len(entries)*fineBytes
	payload := make([]byte, size)
	copy(payload, "FRB2")
	binary.LittleEndian.PutUint32(payload[4:], uint32(len(entries)))
	binary.LittleEndian.PutUint32(payload[8:], secretCoarseGrid)
	binary.LittleEndian.PutUint32(payload[12:], secretFineGrid)
	binary.LittleEndian.PutUint32(payload[16:], 0)
	position := 20
	for _, item := range entries {
		binary.LittleEndian.PutUint32(payload[position:], uint32(item.code))
		position += 4
	}
	packBits := func(values []byte, perItem int) []byte {
		out := make([]byte, len(values)/8)
		for index, value := range values {
			if value != 0 {
				out[index/8] |= 1 << (7 - uint(index%8))
			}
		}
		return out
	}
	coarse := make([]byte, 0, len(entries)*secretCoarseSize)
	fine := make([]byte, 0, len(entries)*secretFineSize)
	for _, item := range entries {
		coarse = append(coarse, item.feature.value[:secretCoarseSize]...)
		fine = append(fine, item.feature.value[secretCoarseSize:]...)
	}
	copy(payload[position:], packBits(coarse, secretCoarseSize))
	position += len(entries) * coarseBytes
	copy(payload[position:], fine)
	return payload, nil
}
