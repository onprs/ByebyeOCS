package chaoxing

import (
	"bytes"
	"context"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxCoverSourceBytes = 2 << 20
	maxCoverBytes       = 256 << 10
	maxCoverPixels      = 4_000_000
	maxCoverSourceSide  = 4096
	maxCoverSide        = 768
)

// normalizeCoverURL 只把可信平台图片地址转换为HTTPS，不接受客户端任意代理地址。
func (p *Platform) normalizeCoverURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	target, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if !target.IsAbs() {
		base, err := url.Parse(p.options.endpoints.CourseList)
		if err != nil {
			return ""
		}
		target = base.ResolveReference(target)
	}
	// 课程列表仍可能给出http图片地址；仅对已允许的无端口平台地址升级。
	if target.Scheme == "http" && target.Port() == "" && p.factory.allowedHost(target.Hostname()) {
		target.Scheme = "https"
	}
	if !p.factory.allowedURL(target) || target.Fragment != "" || len(target.String()) > 2048 {
		return ""
	}
	return target.String()
}

func (p *Platform) FetchCourseCover(ctx context.Context, source string) (learning.CourseCoverImage, error) {
	unavailable := func() (learning.CourseCoverImage, error) {
		return learning.CourseCoverImage{}, learning.Fail(learning.FailureCapabilityMissing, "课程封面暂不可用")
	}
	if !p.options.ReadOnlyEnabled || source == "" || p.normalizeCoverURL(source) != source {
		return unavailable()
	}
	select {
	case p.coverSlots <- struct{}{}:
		defer func() { <-p.coverSlots }()
	case <-ctx.Done():
		return unavailable()
	}
	if err := p.factory.limiter.wait(ctx, p.options.MinRequestInterval); err != nil {
		return unavailable()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return unavailable()
	}
	// 封面是公开静态资源。独立的无Cookie客户端不接触学习会话或请求中的认证头。
	request.Header.Set("User-Agent", "Mozilla/5.0 ByebyeOCS-Cover/1.0")
	request.Header.Set("Accept", "image/jpeg,image/png,image/webp,image/gif")
	request.Header.Set("Referer", "https://mooc2-ans.chaoxing.com/")
	response, err := p.coverClient.Do(request)
	if err != nil {
		return unavailable()
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maxCoverSourceBytes {
		return unavailable()
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxCoverSourceBytes+1))
	if err != nil || len(payload) > maxCoverSourceBytes {
		return unavailable()
	}
	return normalizeCourseCover(payload)
}

func normalizeCourseCover(payload []byte) (learning.CourseCoverImage, error) {
	invalid := learning.Fail(learning.FailureCapabilityMissing, "课程封面格式或尺寸未支持")
	if len(payload) == 0 || len(payload) > maxCoverSourceBytes {
		return learning.CourseCoverImage{}, invalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		config.Width > maxCoverSourceSide || config.Height > maxCoverSourceSide ||
		int64(config.Width)*int64(config.Height) > maxCoverPixels {
		return learning.CourseCoverImage{}, invalid
	}
	switch format {
	case "jpeg", "png", "gif", "webp":
	default:
		return learning.CourseCoverImage{}, invalid
	}
	// GIF只解码首帧；统一重编码为静态JPEG，移除原始元数据并限制客户端解码内存。
	source, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		return learning.CourseCoverImage{}, invalid
	}
	width, height := config.Width, config.Height
	if longest := max(width, height); longest > maxCoverSide {
		width, height = max(1, width*maxCoverSide/longest), max(1, height*maxCoverSide/longest)
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	stddraw.Draw(target, target.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	draw.CatmullRom.Scale(target, target.Bounds(), source, source.Bounds(), stddraw.Over, nil)
	for _, quality := range []int{82, 68, 50} {
		var output bytes.Buffer
		if err := jpeg.Encode(&output, target, &jpeg.Options{Quality: quality}); err != nil {
			return learning.CourseCoverImage{}, invalid
		}
		if output.Len() <= maxCoverBytes {
			return learning.CourseCoverImage{Bytes: output.Bytes()}, nil
		}
	}
	return learning.CourseCoverImage{}, invalid
}
