package dbusx

import (
	"testing"
)

// 4.x GetInfo 返回的 XML 样例（active_session 含格式信息）。
const getInfoSample = `<?xml version="1.0" encoding="UTF-8"?>
<info>
  <version>4.3.5</version>
  <state>active</state>
  <active_session>AirPlay 2, timing protocol: PTP, format: 44100/16 (little-endian), audio mode: playing</active_session>
  <statistics>...</statistics>
</info>`

func TestParseActiveSession(t *testing.T) {
	got := parseActiveSession(getInfoSample)
	want := "AirPlay 2, timing protocol: PTP, format: 44100/16 (little-endian), audio mode: playing"
	if got != want {
		t.Errorf("got %q", got)
	}
	if parseActiveSession("<info><state>active</state></info>") != "" {
		t.Error("无 active_session 应返回空")
	}
}

func TestParseFormat(t *testing.T) {
	cases := []struct {
		in          string
		wantRate    int
		wantDepth   int
		wantCh      int
	}{
		// 4.x active_session（format: 前缀 + 描述）
		{"AirPlay 2, timing protocol: PTP, format: 44100/16 (little-endian), audio mode: playing", 44100, 16, 0},
		{"AirPlay, format: 96000/24, audio mode: playing", 96000, 24, 0},
		// 5.x SourceFormat（sdsc 码：编码/采样率/位深/声道）
		{"ALAC/44100/S16_LE/2", 44100, 16, 2},
		{"AAC/44100/S16_LE/2", 44100, 16, 2},
		{"AAC/48000/S16_LE/2", 48000, 16, 2},
		// 5.x OutputFormat（odsc 码）
		{"44100/S32_LE/2", 44100, 32, 2},
		// pipe asfm 码
		{"44100/16", 44100, 16, 0},
		{"96000/24", 96000, 24, 0},
		// 无格式信息
		{"无格式信息", 0, 0, 0},
		{"", 0, 0, 0},
	}
	for _, c := range cases {
		rate, depth, ch := parseFormat(c.in)
		if rate != c.wantRate || depth != c.wantDepth || ch != c.wantCh {
			t.Errorf("parseFormat(%q) = %d/%d/%d, want %d/%d/%d",
				c.in, rate, depth, ch, c.wantRate, c.wantDepth, c.wantCh)
		}
	}
}

func TestJoinArtists(t *testing.T) {
	if got := joinArtists("张三;李四;"); got != "张三 / 李四" {
		t.Errorf("got %q", got)
	}
	if got := joinArtists("单人"); got != "单人" {
		t.Errorf("got %q", got)
	}
}

func TestIntrospectNodeParse(t *testing.T) {
	xml := `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN"
"http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
<node>
  <interface name="org.gnome.ShairportSync">
    <method name="RemoteCommand"/>
    <property name="Active" type="b" access="read"/>
  </interface>
  <interface name="org.gnome.ShairportSync.RemoteControl">
    <method name="GetInfo"/>
    <method name="SetAirplayVolume">
      <arg name="volume" type="d" direction="in"/>
    </method>
    <property name="PlayerState" type="s" access="read"/>
  </interface>
</node>`
	var node introspectNode
	if err := xmlUnmarshal([]byte(xml), &node); err != nil {
		t.Fatal(err)
	}
	found := false
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
	if !found {
		t.Error("未找到 GetInfo 方法")
	}
}

// 无 D-Bus 服务时 GetPlayerStatus/GetTrack 必须返回零值而非 panic。
// WSL 的 system bus 上通常没有运行中的 shairport 实例，此测试在
// 有/无服务两种情况下都应通过。
func TestGetStatusDegraded(t *testing.T) {
	c := New("")
	st := c.GetPlayerStatus()
	if st.Available {
		// 环境里恰好有服务时，零值字段也应合理
		if st.VersionString == "" && st.HasGetInfo {
			t.Error("有服务但 VersionString 为空")
		}
	}
	track := c.GetTrack()
	_ = track // 结构体零值即可，不 panic 就算通过
}
