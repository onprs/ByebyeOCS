package chaoxing

import (
	"errors"
	"math"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// sfntCurveSteps 控制曲线采样密度；固定步数保证同一字体每次得到相同点序列。
const sfntCurveSteps = 8

// sfntContours 用标准解析器读取字形轮廓，支持标准解析器可读取的 glyf 与 CFF。
// 曲线按固定步数展开为折线，输出与 TrueType 轮廓相同的点结构。
// LoadGlyph 的 Y 轴向下，这里翻转回字体坐标，保证与 glyf 特征一致。
func (f *secretFont) sfntContours(gid uint16) ([][]secretGlyphPoint, error) {
	var buffer sfnt.Buffer
	segments, err := f.sfnt.LoadGlyph(&buffer, sfnt.GlyphIndex(gid),
		fixed.Int26_6(f.unitsPerEm)*64, nil)
	if err != nil {
		return nil, errors.New("字形轮廓无法读取")
	}
	contours := make([][]secretGlyphPoint, 0, 4)
	current := make([]secretGlyphPoint, 0, 32)
	var x, y float64
	appendPoint := func(px, py float64) {
		current = append(current, secretGlyphPoint{
			x: int32(math.Round(px)), y: int32(math.Round(py)), onCurve: true})
	}
	flush := func() {
		if len(current) >= 2 {
			contours = append(contours, current)
		}
		current = make([]secretGlyphPoint, 0, 32)
	}
	point := func(value fixed.Point26_6) (float64, float64) {
		return float64(value.X) / 64, -float64(value.Y) / 64
	}
	for _, segment := range segments {
		switch segment.Op {
		case sfnt.SegmentOpMoveTo:
			flush()
			x, y = point(segment.Args[0])
			appendPoint(x, y)
		case sfnt.SegmentOpLineTo:
			x, y = point(segment.Args[0])
			appendPoint(x, y)
		case sfnt.SegmentOpQuadTo:
			cx, cy := point(segment.Args[0])
			ex, ey := point(segment.Args[1])
			for step := 1; step <= sfntCurveSteps; step++ {
				t := float64(step) / float64(sfntCurveSteps)
				inv := 1 - t
				appendPoint(inv*inv*x+2*inv*t*cx+t*t*ex, inv*inv*y+2*inv*t*cy+t*t*ey)
			}
			x, y = ex, ey
		case sfnt.SegmentOpCubeTo:
			c1x, c1y := point(segment.Args[0])
			c2x, c2y := point(segment.Args[1])
			ex, ey := point(segment.Args[2])
			for step := 1; step <= sfntCurveSteps; step++ {
				t := float64(step) / float64(sfntCurveSteps)
				inv := 1 - t
				appendPoint(
					inv*inv*inv*x+3*inv*inv*t*c1x+3*inv*t*t*c2x+t*t*t*ex,
					inv*inv*inv*y+3*inv*inv*t*c1y+3*inv*t*t*c2y+t*t*t*ey)
			}
			x, y = ex, ey
		}
	}
	flush()
	if len(contours) == 0 {
		return nil, errors.New("字形轮廓为空")
	}
	return contours, nil
}
