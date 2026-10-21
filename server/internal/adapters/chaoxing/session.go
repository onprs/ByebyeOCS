package chaoxing

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Session 是认证服务加密保存、业务服务按需解密的平台会话材料。
// 不包含密码、课程参数或任意可伪造的主体字段。
type Session struct {
	Cookies    []Cookie `json:"cookies"`
	UserAgent  string   `json:"user_agent"`
	Authorized bool     `json:"authorized_by_user"`
}

type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
	Expires  float64 `json:"expires"`
}

// DecodeSession 供服务内部其他适配器复用会话格式校验；不向客户端暴露。
func DecodeSession(raw []byte) (Session, error) { return decodeSession(raw) }

func decodeSession(raw []byte) (Session, error) {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return Session{}, errors.New("平台会话为空或过大")
	}
	var session Session
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&session); err != nil {
		return Session{}, errors.New("平台会话格式无效")
	}
	if !session.Authorized || strings.TrimSpace(session.UserAgent) == "" || len(session.Cookies) == 0 {
		return Session{}, errors.New("平台会话缺少授权标记或客户端上下文")
	}
	if len(session.UserAgent) > 512 {
		return Session{}, errors.New("平台会话客户端上下文无效")
	}
	for _, cookie := range session.Cookies {
		if !validCookie(cookie) {
			return Session{}, errors.New("平台会话包含无效 Cookie")
		}
	}
	return session, nil
}

func validCookie(cookie Cookie) bool {
	if cookie.Name == "" || len(cookie.Name) > 128 || len(cookie.Value) > 8192 {
		return false
	}
	for _, r := range cookie.Name + cookie.Value + cookie.Domain + cookie.Path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
	return domain != ""
}

func encodeSession(session Session) ([]byte, error) {
	if !session.Authorized || session.UserAgent == "" || len(session.Cookies) == 0 {
		return nil, errors.New("平台会话不完整")
	}
	return json.Marshal(session)
}

func sessionJar(session Session) (http.CookieJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	for _, entry := range session.Cookies {
		domain := strings.TrimPrefix(strings.ToLower(entry.Domain), ".")
		target, err := url.Parse("https://" + domain)
		if err != nil {
			return nil, errors.New("平台 Cookie 域名无效")
		}
		cookie := &http.Cookie{
			Name: entry.Name, Value: entry.Value, Domain: entry.Domain,
			Path: entry.Path, Secure: entry.Secure, HttpOnly: entry.HTTPOnly,
		}
		if entry.Expires > 0 {
			cookie.Expires = time.Unix(int64(entry.Expires), 0)
		}
		jar.SetCookies(target, []*http.Cookie{cookie})
	}
	return jar, nil
}

// recordingJar 在标准 CookieJar 之外记录平台实际下发的域和路径，
// 避免导出时把 host-only Cookie 错配到父域。
type recordingJar struct {
	inner http.CookieJar
	mu    sync.Mutex
	items map[string]Cookie
	order []string
}

func newRecordingJar() (*recordingJar, error) {
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &recordingJar{inner: inner, items: map[string]Cookie{}}, nil
}

func (r *recordingJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
	r.inner.SetCookies(target, cookies)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cookie := range cookies {
		domain := cookie.Domain
		if domain == "" {
			domain = target.Hostname()
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		key := cookie.Name + "|" + domain + "|" + path
		if _, exists := r.items[key]; !exists {
			r.order = append(r.order, key)
		}
		r.items[key] = Cookie{Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path,
			Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly,
			Expires: float64(cookie.Expires.Unix())}
	}
}

func (r *recordingJar) Cookies(target *url.URL) []*http.Cookie { return r.inner.Cookies(target) }

func (r *recordingJar) export() []Cookie {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Cookie, 0, len(r.order))
	for _, key := range r.order {
		cookie := r.items[key]
		if !validCookie(cookie) {
			continue
		}
		result = append(result, cookie)
	}
	return result
}
