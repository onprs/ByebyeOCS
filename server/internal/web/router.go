package web

import (
	"net/http"
	"strings"
)

// Router 让未匹配路由与方法也遵守 JSON 错误契约。
func Router(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		allowed := []string{}
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			candidate := r.Clone(r.Context())
			candidate.Method = method
			if _, pattern := mux.Handler(candidate); pattern != "" {
				allowed = append(allowed, method)
			}
		}
		if len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			Error(w, r, 405, "method_not_allowed", "此接口不支持当前请求方法")
			return
		}
		Error(w, r, 404, "not_found", "请求的接口不存在")
	})
}
