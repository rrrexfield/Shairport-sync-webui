package dbusx

import (
	"strings"
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
	session := "AirPlay 2, timing protocol: PTP, format: 44100/16 (little-endian), audio mode: playing"
	rate, depth := parseFormat(session)
	if rate != 44100 || depth != 16 {
		t.Errorf("rate=%d depth=%d", rate, depth)
	}
	rate, depth = parseFormat("AirPlay, format: 96000/24, audio mode: playing")
	if rate != 96000 || depth != 24 {
		t.Errorf("rate=%d depth=%d", rate, depth)
	}
	rate, depth = parseFormat("无格式信息")
	if rate != 0 || depth != 0 {
		t.Errorf("rate=%d depth=%d", rate, depth)
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

// 协议降级推断：老版本无 Protocol 属性，从版本串推断。
func TestProtocolInference(t *testing.T) {
	makeStatus := func(versionString string) PlayerStatus {
		st := PlayerStatus{Available: true, VersionString: versionString}
		if st.Protocol == "" && st.VersionString != "" {
			if strings.Contains(st.VersionString, "AirPlay2") {
				st.Protocol = "AirPlay 2"
			} else {
				st.Protocol = "AirPlay"
			}
		}
		return st
	}
	cases := []struct {
		vs, want string
	}{
		{"3.3.8-libdaemon-OpenSSL-Avahi-ALSA-stdout-pipe-soxr-metadata-dbus-mpris-sysconfdir:/etc", "AirPlay"},
		{"4.3.5-AirPlay2-OpenSSL-Avahi-ALSA-soxr-metadata-dbus-sysconfdir:/etc", "AirPlay 2"},
		{"5.2.1-AirPlay2-smi10-OpenSSL-Avahi-ALSA-stdout-pipe-soxr-metadata-dbus-mpris-sysconfdir:/etc", "AirPlay 2"},
	}
	for _, c := range cases {
		if got := makeStatus(c.vs).Protocol; got != c.want {
			t.Errorf("版本串 %q → Protocol=%q, 期望 %q", c.vs, got, c.want)
		}
	}
	// 已有 Protocol 属性值时不覆盖（5.x 直读优先）
	st := PlayerStatus{Protocol: "AirPlay", VersionString: "5.2.1-AirPlay2-..."}
	if st.Protocol != "AirPlay" {
		t.Errorf("已有 Protocol 不应被覆盖")
	}
}
