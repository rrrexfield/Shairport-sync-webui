// Package api 提供 HTTP REST 接口。
package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"shairport-webui/internal/app"
)

// Server 是 API 服务器。
type Server struct {
	App    *app.App
	Static http.Handler
}

// NewServer 创建服务器。
func NewServer(a *app.App, static http.Handler) *Server {
	return &Server{App: a, Static: static}
}

// Handler 返回路由（带日志中间件）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/service/start", s.handleService("start"))
	mux.HandleFunc("/api/service/stop", s.handleService("stop"))
	mux.HandleFunc("/api/service/restart", s.handleService("restart"))
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/reset", s.handleConfigReset)
	mux.HandleFunc("/api/config/raw", s.handleConfigRaw)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/capabilities", s.handleCapabilities)
	mux.Handle("/", s.Static)
	return logMiddleware(mux)
}

// ---- 通用 JSON 工具 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// logMiddleware 记录请求方法与路径，panic 恢复。
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic on %s %s: %v", r.Method, r.URL.Path, rec)
				writeErr(w, http.StatusInternalServerError, "内部错误")
			}
		}()
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}
