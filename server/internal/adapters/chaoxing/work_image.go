package chaoxing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

const (
	maxWorkImageBytes        = 2 << 20
	maxWorkImageTotalBytes   = 16 << 20
	maxWorkImageSide         = 4096
	maxWorkImagePixels       = 12_000_000
	maxWorkImagesPerQuestion = 6
	maxWorkImagesPerSheet    = 24
)

type workImageRef struct {
	ID  string
	URL string
}

func workImageReference(source string) (workImageRef, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return workImageRef{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片地址为空")
	}
	lower := strings.ToLower(source)
	if strings.HasPrefix(lower, "data:") {
		comma := strings.IndexByte(source, ',')
		if comma < 0 {
			return workImageRef{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片数据无效")
		}
		sum := sha256.Sum256([]byte(source))
		return workImageRef{ID: hex.EncodeToString(sum[:8]), URL: source}, nil
	}
	if strings.HasPrefix(source, "//") {
		source = "https:" + source
	}
	target, err := url.Parse(source)
	if err != nil {
		return workImageRef{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片地址无效")
	}
	if !target.IsAbs() {
		return workImageRef{}, nil
	}
	if target.Scheme != "https" || target.User != nil || target.Fragment != "" {
		return workImageRef{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片地址不在允许范围")
	}
	// 内部引用标识按完整 URL 区分；试卷指纹另按实际图片内容计算。
	stable := target.String()
	sum := sha256.Sum256([]byte(stable))
	return workImageRef{ID: hex.EncodeToString(sum[:8]), URL: target.String()}, nil
}

func workImageContentHash(image learning.WorkImage) string {
	raw, err := base64.StdEncoding.DecodeString(image.Base64)
	if err != nil || len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func workImageRawSize(image learning.WorkImage) int {
	size := base64.StdEncoding.DecodedLen(len(image.Base64))
	if strings.HasSuffix(image.Base64, "==") {
		return size - 2
	}
	if strings.HasSuffix(image.Base64, "=") {
		return size - 1
	}
	return size
}

func workImageBudgetExceeded(total, raw int) bool {
	return total < 0 || raw < 0 || total+raw > maxWorkImageTotalBytes
}

func resolveWorkImageReference(source, baseURL string) (workImageRef, error) {
	source = strings.TrimSpace(source)
	lower := strings.ToLower(source)
	if strings.HasPrefix(lower, "data:") || strings.HasPrefix(source, "//") {
		return workImageReference(source)
	}
	target, err := url.Parse(source)
	if err != nil {
		return workImageRef{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片地址无效")
	}
	if !target.IsAbs() {
		base, parseErr := url.Parse(baseURL)
		if parseErr != nil || base.Scheme != "https" || base.Host == "" {
			return workImageRef{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片缺少可信基准地址")
		}
		target = base.ResolveReference(target)
	}
	return workImageReference(target.String())
}

func (p *Platform) fetchWorkImage(ctx context.Context, session Session, ref workImageRef, referer string) (learning.WorkImage, error) {
	if strings.HasPrefix(strings.ToLower(ref.URL), "data:") {
		return decodeWorkDataImage(ref)
	}
	target, err := url.Parse(ref.URL)
	if err != nil || !p.factory.allowedURL(target) || target.Fragment != "" {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片地址不在允许范围")
	}
	jar, err := sessionJar(session)
	if err != nil {
		return learning.WorkImage{}, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法建立 Cookie 容器")
	}
	client := p.factory.client(jar)
	defer client.CloseIdleConnections()
	limit := int64(maxWorkImageBytes)
	if p.options.MaxResponseBytes > 0 && p.options.MaxResponseBytes < limit {
		limit = p.options.MaxResponseBytes
	}
	if err := p.factory.limiter.wait(ctx, p.options.MinRequestInterval); err != nil {
		return learning.WorkImage{}, learning.Fail(learning.FailureTransient, "测验图片读取等待被取消")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return learning.WorkImage{}, learning.Fail(learning.FailurePermanent, "无法建立测验图片请求")
	}
	request.Header.Set("User-Agent", session.UserAgent)
	request.Header.Set("Accept", "image/png,image/jpeg,image/gif;q=0.9,*/*;q=0.1")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if referer != "" {
		if parsed, parseErr := url.Parse(referer); parseErr == nil && p.factory.allowedURL(parsed) {
			request.Header.Set("Referer", parsed.String())
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return learning.WorkImage{}, learning.Fail(learning.FailureTransient, "测验图片读取未完成")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return learning.WorkImage{}, learning.Fail(learning.FailureIdentityInvalid, "平台身份已失效，无法读取测验图片")
	case http.StatusForbidden:
		return learning.WorkImage{}, learning.Fail(learning.FailureForbidden, "平台拒绝读取测验图片")
	case http.StatusTooManyRequests:
		return learning.WorkImage{}, learning.Fail(learning.FailureRateLimited, "平台限流，已停止读取测验图片")
	case http.StatusOK:
	default:
		return learning.WorkImage{}, learning.Fail(learning.FailureTransient, "测验图片读取未成功")
	}
	if length, parseErr := strconv.ParseInt(strings.TrimSpace(response.Header.Get("Content-Length")), 10, 64); parseErr == nil && length > limit {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片超出允许体积")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(payload)) > limit {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片不可用或超出允许体积")
	}
	return validateWorkImage(payload, limit, ref.ID)
}

func decodeWorkDataImage(ref workImageRef) (learning.WorkImage, error) {
	comma := strings.IndexByte(ref.URL, ',')
	if comma < 0 {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片数据无效")
	}
	header := strings.ToLower(ref.URL[:comma])
	if !strings.Contains(header, ";base64") {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片编码未支持")
	}
	raw, err := base64.StdEncoding.DecodeString(ref.URL[comma+1:])
	if err != nil {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片编码无效")
	}
	if len(raw) > maxWorkImageBytes {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片超出允许体积")
	}
	return validateWorkImage(raw, int64(maxWorkImageBytes), ref.ID)
}

func validateWorkImage(payload []byte, limit int64, id string) (learning.WorkImage, error) {
	if int64(len(payload)) > limit || len(payload) > maxWorkImageBytes {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片超出允许体积")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片格式未支持")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxWorkImageSide ||
		config.Height > maxWorkImageSide || int64(config.Width)*int64(config.Height) > maxWorkImagePixels {
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片尺寸超出限制")
	}
	mimeType := ""
	switch format {
	case "png":
		mimeType = "image/png"
	case "jpeg":
		mimeType = "image/jpeg"
	case "gif":
		mimeType = "image/gif"
	default:
		return learning.WorkImage{}, learning.Fail(learning.FailureCapabilityMissing, "测验图片格式未支持")
	}
	return learning.WorkImage{ID: id, MIMEType: mimeType,
		Base64: base64.StdEncoding.EncodeToString(payload)}, nil
}
