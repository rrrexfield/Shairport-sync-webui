// Package dbusx 封装 shairport-sync 的 D-Bus 接口（system bus）。
//
// 设计原则：任何 D-Bus 错误都返回零值 + Available=false，绝不让
// D-Bus 故障变成 API 错误。连接采用惰性重连：失败后冷却 10s 再试，
// 兼容 shairport-sync 重启后 bus 名消失/重建。
//
// 版本适配：
//   - 3.3.x：RemoteControl 属性（PlayerState 等）+ MPRIS（曲目）
//   - 4.x：额外支持 GetInfo 方法（返回含采样率/位深的 active_session）
package dbusx

import (
	"encoding/xml"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	nativeBus    = "org.gnome.ShairportSync"
	nativePath   = "/org/gnome/ShairportSync"
	remoteIface  = "org.gnome.ShairportSync.RemoteControl"
	mprisBus     = "org.mpris.MediaPlayer2.ShairportSync"
	mprisPath    = "/org/mpris/MediaPlayer2"
	mprisPlayer  = "org.mpris.MediaPlayer2.Player"
	coolDown     = 10 * time.Second
	callTimeout  = 1500 * time.Millisecond
)

// PlayerStatus 是播放器状态视图。
type PlayerStatus struct {
	Available     bool    `json:"available"`
	State         string  `json:"state"` // PlayerState / active_session 派生
	Client        string  `json:"client"`
	Progress      string  `json:"progress"`
	AirplayVolume float64 `json:"airplay_volume"`
	Version       string  `json:"version"`
	VersionString string  `json:"version_string"`
	HasGetInfo    bool    `json:"has_getinfo"`
	ActiveSession string  `json:"active_session"` // 4.x GetInfo 原始会话描述
	SampleRate    int     `json:"sample_rate"`    // 从 active_session/SourceFormat 解析
	BitDepth      int     `json:"bit_depth"`
	// 5.x 顶层接口新增属性
	Protocol     string `json:"protocol"`      // "AirPlay" / "AirPlay 2"
	ServiceName  string `json:"service_name"`  // 服务显示名
	SourceFormat string `json:"source_format"` // 源音质，如 "44100/16"
	OutputFormat string `json:"output_format"` // 输出格式
}

// TrackInfo 是曲目信息（MPRIS 优先，pipe 互补）。
type TrackInfo struct {
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	Genre      string `json:"genre"`
	ArtURL     string `json:"art_url"`
	TrackID    string `json:"track_id"`
	DurationMs int64  `json:"duration_ms"`
}

// Client 是惰性连接的 D-Bus 客户端。
type Client struct {
	mu          sync.Mutex
	conn        *dbus.Conn
	lastTry     time.Time
	lastErr     string
	hasGetInfo  int8 // -1 未探测 / 0 无 / 1 有
	dbusAddress string
}

// New 创建客户端。dbusAddress 为空用系统总线（调试可传自定义地址）。
func New(dbusAddress string) *Client {
	return &Client{hasGetInfo: -1, dbusAddress: dbusAddress}
}

// connOrNil 获取可用连接（惰性 + 冷却），不可用返回 nil。
func (c *Client) connOrNil() *dbus.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn
	}
	if time.Since(c.lastTry) < coolDown {
		return nil
	}
	c.lastTry = time.Now()
	var conn *dbus.Conn
	var err error
	if c.dbusAddress != "" {
		conn, err = dbus.Dial(c.dbusAddress)
	} else {
		conn, err = dbus.SystemBus()
	}
	if err != nil {
		c.lastErr = err.Error()
		return nil
	}
	c.conn = conn
	c.lastErr = ""
	return conn
}

// dropConn 记录错误并断开连接（下次访问进入冷却期）。
func (c *Client) dropConn(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.lastErr = err.Error()
	c.lastTry = time.Now()
}

// LastError 返回最近一次错误（诊断展示）。
func (c *Client) LastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// obj 返回 native 对象（conn 为 nil 时返回 nil）。
func (c *Client) obj() dbus.BusObject {
	conn := c.connOrNil()
	if conn == nil {
		return nil
	}
	return conn.Object(nativeBus, nativePath)
}

// getString 读取属性字符串，失败返回零值。
func getString(obj dbus.BusObject, iface, prop string) string {
	if obj == nil {
		return ""
	}
	v, err := obj.GetProperty(iface + "." + prop)
	if err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

// getFloat64 读取属性浮点值。
func getFloat64(obj dbus.BusObject, iface, prop string) float64 {
	if obj == nil {
		return 0
	}
	v, err := obj.GetProperty(iface + "." + prop)
	if err != nil {
		return 0
	}
	f, _ := v.Value().(float64)
	return f
}

// GetPlayerStatus 读取播放器状态（永不返回 error）。
func (c *Client) GetPlayerStatus() PlayerStatus {
	obj := c.obj()
	if obj == nil {
		return PlayerStatus{}
	}
	st := PlayerStatus{
		Available:     true,
		State:         getString(obj, remoteIface, "PlayerState"),
		Client:        getString(obj, remoteIface, "Client"),
		Progress:      getString(obj, remoteIface, "ProgressString"),
		AirplayVolume: getFloat64(obj, remoteIface, "AirplayVolume"),
		Version:       getString(obj, "org.gnome.ShairportSync", "Version"),
		VersionString: getString(obj, "org.gnome.ShairportSync", "VersionString"),
		HasGetInfo:    c.hasGetInfoMethod(obj),
		// 5.x 顶层新增属性（3.3.8/4.x 读取失败返回零值，天然降级）
		Protocol:     getString(obj, "org.gnome.ShairportSync", "Protocol"),
		ServiceName:  getString(obj, "org.gnome.ShairportSync", "ServiceName"),
		SourceFormat: getString(obj, "org.gnome.ShairportSync", "SourceFormat"),
		OutputFormat: getString(obj, "org.gnome.ShairportSync", "OutputFormat"),
	}
	// 音质信息：5.x SourceFormat 优先
	if st.SourceFormat != "" {
		if rate, depth := parseFormat(st.SourceFormat); rate > 0 {
			st.SampleRate, st.BitDepth = rate, depth
		}
	}
	// 4.x GetInfo 兼容
	if st.HasGetInfo {
		if xml := c.callGetInfo(obj); xml != "" {
			st.ActiveSession = parseActiveSession(xml)
			if st.ActiveSession != "" {
				st.State = "playing"
				if st.SampleRate == 0 {
					st.SampleRate, st.BitDepth = parseFormat(st.ActiveSession)
				}
			}
		}
	}
	return st
}

// hasGetInfoMethod 一次性探测 RemoteControl 是否有 GetInfo 方法（4.x 特性）。
func (c *Client) hasGetInfoMethod(obj dbus.BusObject) bool {
	c.mu.Lock()
	if c.hasGetInfo >= 0 {
		cached := c.hasGetInfo
		c.mu.Unlock()
		return cached == 1
	}
	c.mu.Unlock()

	found := false
	// 调用 org.freedesktop.DBus.Introspectable.Introspect 拿 XML
	call := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0)
	if call.Err == nil {
		var xml string
		if err := call.Store(&xml); err == nil {
			var node introspectNode
			if xmlErr := xmlUnmarshal([]byte(xml), &node); xmlErr == nil {
				for _, iface := range node.Interfaces {
					if iface.Name != remoteIface {
						continue
					}
					for _, m := range iface.Methods {
						if m.Name == "GetInfo" {
							found = true
						}
					}
				}
			}
		}
	}
	c.mu.Lock()
	if found {
		c.hasGetInfo = 1
	} else {
		c.hasGetInfo = 0
	}
	c.mu.Unlock()
	return found
}

// callGetInfo 调用 4.x 的 GetInfo（返回 XML 字符串）。
func (c *Client) callGetInfo(obj dbus.BusObject) string {
	call := obj.Call(remoteIface+".GetInfo", 0)
	if call.Err != nil {
		return ""
	}
	var xml string
	if err := call.Store(&xml); err != nil {
		return ""
	}
	return xml
}

// GetTrack 读取 MPRIS 曲目信息（永不返回 error）。
func (c *Client) GetTrack() TrackInfo {
	conn := c.connOrNil()
	if conn == nil {
		return TrackInfo{}
	}
	obj := conn.Object(mprisBus, mprisPath)
	var t TrackInfo
	// 读取失败整个放弃（不逐字段降级，避免半截信息）
	mdV, err := obj.GetProperty(mprisPlayer + ".Metadata")
	if err != nil {
		return t
	}
	md, ok := mdV.Value().(map[string]dbus.Variant)
	if !ok {
		return t
	}
	get := func(key string) string {
		if v, ok := md[key]; ok {
			if s, ok := v.Value().(string); ok {
				return s
			}
		}
		return ""
	}
	t.Title = get("xesam:title")
	t.Artist = joinArtists(get("xesam:artist"))
	t.Album = get("xesam:album")
	t.Genre = get("xesam:genre")
	t.ArtURL = get("mpris:artUrl")
	t.TrackID = get("mpris:trackid")
	if v, ok := md["mpris:length"]; ok {
		if n, ok := v.Value().(int64); ok {
			t.DurationMs = n / 1000
		}
	}
	return t
}

// joinArtists 处理 xesam:artist 的 "a;b" 拼接格式。
func joinArtists(s string) string {
	if !strings.Contains(s, ";") {
		return s
	}
	parts := strings.Split(s, ";")
	nonEmpty := parts[:0]
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, " / ")
}

// ---- Introspect XML 解析 ----

// introspectNode 是 org.freedesktop.DBus.Introspectable.Introspect 返回的 XML 结构（子集）。
type introspectNode struct {
	XMLName    xml.Name `xml:"node"`
	Interfaces []struct {
		Name    string `xml:"name,attr"`
		Methods []struct {
			Name string `xml:"name,attr"`
		} `xml:"method"`
	} `xml:"interface"`
}

// xmlUnmarshal 便于测试替换（无实际差异，保持与 encoding/xml 一致签名）。
var xmlUnmarshal = xml.Unmarshal

// ---- 4.x active_session 解析 ----

var reActiveSession = regexp.MustCompile(`<active_session>([^<]*)</active_session>`)
var reFormat = regexp.MustCompile(`format:\s*(\d+)\s*/\s*(\d+)`)

// parseActiveSession 从 GetInfo 返回的 XML 中提取 active_session。
func parseActiveSession(xml string) string {
	m := reActiveSession.FindStringSubmatch(xml)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// parseFormat 从会话描述中解析采样率与位深（如 "format: 44100/16"）。
func parseFormat(session string) (rate, depth int) {
	m := reFormat.FindStringSubmatch(session)
	if m == nil {
		return 0, 0
	}
	rate = atoi(m[1])
	depth = atoi(m[2])
	return rate, depth
}

func atoi(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
