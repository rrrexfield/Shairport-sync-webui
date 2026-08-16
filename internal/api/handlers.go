package api

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"shairport-webui/internal/config"
	"shairport-webui/internal/volume"
)

// handleStatus GET /api/status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, s.App.Status(r.Context()))
}

// handleService POST /api/service/{start,stop,restart}
func (s *Server) handleService(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		var err error
		switch action {
		case "start":
			err = s.App.Service.Start(ctx)
		case "stop":
			err = s.App.Service.Stop(ctx)
		case "restart":
			err = s.App.Service.Restart(ctx)
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action})
	}
}

// handleConfig GET/PUT /api/config
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.configView())
	case http.MethodPut:
		var req struct {
			Changes []config.Change `json:"changes"`
		}
		if err := readJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		if len(req.Changes) == 0 {
			writeErr(w, http.StatusBadRequest, "changes 为空")
			return
		}
		backup, err := s.App.Config.ApplyChanges(req.Changes)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"backup":       backup,
			"need_restart": true, // conf 修改后重启服务生效
			"message":      "配置已保存，重启 shairport-sync 服务后生效",
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
	}
}

// configView 组装 schema × 当前值视图。
func (s *Server) configView() map[string]any {
	f, err := s.App.Config.Load()
	fields := make([]map[string]any, 0, len(config.Schema))
	sections := []string{}
	if err == nil {
		sections = f.Sections
	}
	for _, def := range config.AllDefs() {
		item := map[string]any{
			"section":  def.Section,
			"key":      def.Key,
			"type":     def.Type,
			"label":    def.Label,
			"group":    def.Group,
			"default":  def.Default,
			"enum":     def.Enum,
			"hint":     def.Hint,
			"editable": err == nil && f.HasSection(def.Section),
		}
		item["value"] = ""
		item["commented"] = true
		if err == nil {
			if st, ok := f.Get(def.Section, def.Key); ok {
				item["value"] = st.Value
				item["commented"] = st.Commented
			}
		}
		fields = append(fields, item)
	}
	return map[string]any{
		"path":     s.App.Config.Path(),
		"sections": sections,
		"fields":   fields,
	}
}

// handleConfigReset POST /api/config/reset：一键恢复默认配置。
func (s *Server) handleConfigReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	backup, err := s.App.Config.ResetToDefaults()
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"backup":       backup,
		"need_restart": true,
		"message":      "已恢复默认配置，重启 shairport-sync 服务后生效",
	})
}

// handleConfigRaw GET/PUT /api/config/raw
func (s *Server) handleConfigRaw(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		f, err := s.App.Config.Load()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取配置失败: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(f.Render())
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
			return
		}
		backup, err := s.App.Config.ReplaceRaw(body)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"backup":       backup,
			"need_restart": true,
			"message":      "配置已保存，重启 shairport-sync 服务后生效",
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
	}
}

// handleDevices GET /api/devices：ALSA 输出设备 + 混音器控制列表。
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	out := map[string]any{
		"ok": true,
		"alsa_devices":  listAlsaDevices(ctx),
		"mixer_controls": nil,
		"amixer_ok":     false,
	}
	if controls, err := volume.ListControls(ctx, ""); err == nil {
		out["mixer_controls"] = controls
		out["amixer_ok"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// listAlsaDevices 解析 aplay -L 输出（失败返回 nil）。
// aplay -L 每个设备两行：设备名顶格，描述行以空格缩进开头。
func listAlsaDevices(ctx context.Context) []string {
	path, err := exec.LookPath("aplay")
	if err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, path, "-L").Output()
	if err != nil {
		return nil
	}
	var devices []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue // 空行或缩进的描述行
		}
		devices = append(devices, line)
	}
	return devices
}

// handleCapabilities GET /api/capabilities：环境能力探测。
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	_, amixerErr := exec.LookPath("amixer")
	cap := map[string]any{
		"amixer_available": amixerErr == nil,
	}
	// shairport-sync 版本串（编译特性）
	if out, err := exec.Command("shairport-sync", "-V").Output(); err == nil {
		vstr := strings.TrimSpace(string(out))
		cap["shairport_version_string"] = vstr
		cap["features"] = parseFeatures(vstr)
	}
	// D-Bus 能力（取一次 player 状态观察是否有服务）
	st := s.App.Dbus.GetPlayerStatus()
	cap["dbus_available"] = st.Available
	cap["getinfo_supported"] = st.HasGetInfo
	cap["meta_pipe_exists"] = s.App.Pipe.State().Open
	writeJSON(w, http.StatusOK, cap)
}

// parseFeatures 从 -V 输出解析编译特性列表。
func parseFeatures(vstr string) []string {
	parts := strings.Split(vstr, "-")
	if len(parts) < 2 {
		return nil
	}
	feats := parts[1:]
	sort.Strings(feats)
	return feats
}
