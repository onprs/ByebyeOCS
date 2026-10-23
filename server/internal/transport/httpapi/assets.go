package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func assetHandler(directory string) http.Handler {
	if directory == "" {
		return nil
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			web.Error(w, r, 405, "method_not_allowed", "静态页面仅支持 GET 和 HEAD")
			return
		}
		requested := strings.TrimPrefix(r.URL.Path, "/")
		if strings.Contains(requested, "\\") {
			web.Error(w, r, 404, "not_found", "页面不存在")
			return
		}
		for _, part := range strings.Split(requested, "/") {
			if strings.HasPrefix(part, ".") {
				web.Error(w, r, 404, "not_found", "页面不存在")
				return
			}
		}
		full := filepath.Join(root, filepath.FromSlash(requested))
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			if filepath.Ext(requested) != "" {
				web.Error(w, r, 404, "not_found", "资源不存在")
				return
			}
			full = filepath.Join(root, "index.html")
			info, err = os.Stat(full)
			if err != nil || info.IsDir() {
				web.Error(w, r, 404, "not_found", "客户端尚未安装")
				return
			}
		}
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			web.Error(w, r, 404, "not_found", "资源不存在")
			return
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			web.Error(w, r, 404, "not_found", "资源不存在")
			return
		}
		file, err := os.Open(resolved)
		if err != nil {
			web.Error(w, r, 404, "not_found", "资源不存在")
			return
		}
		defer file.Close()
		// Flutter大资源和慢速首屏下载需要独立输出期限，普通API仍使用服务默认值。
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
		w.Header().Del("Content-Type")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
	})
}
