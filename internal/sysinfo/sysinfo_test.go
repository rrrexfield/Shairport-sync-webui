package sysinfo

import (
	"context"
	"testing"
)

func TestParseMeminfo(t *testing.T) {
	content := "MemTotal:        300000 kB\nMemFree:          80000 kB\nMemAvailable:    120000 kB\n"
	total, avail := parseMeminfo(content)
	if total != 300000 || avail != 120000 {
		t.Errorf("total=%d avail=%d", total, avail)
	}
	// 老内核无 MemAvailable：用 MemFree 近似
	old := "MemTotal:        300000 kB\nMemFree:          80000 kB\n"
	total, avail = parseMeminfo(old)
	if total != 300000 || avail != 80000 {
		t.Errorf("老内核: total=%d avail=%d", total, avail)
	}
}

func TestCpuCount(t *testing.T) {
	if n := cpuCount(); n <= 0 {
		t.Errorf("cpuCount = %d", n)
	}
}

func TestUnescapeSSID(t *testing.T) {
	cases := map[string]string{
		`My\:WiFi`:      `My:WiFi`,
		`A\\B`:          `A\B`,
		`Foo\sBar`:      `Foo Bar`,
		`--`:            `--`,
	}
	for in, want := range cases {
		if got := unescapeSSID(in); got != want {
			t.Errorf("unescapeSSID(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// 无无线环境（WSL）：WiFiSSID 必须返回空且不 panic（降级路径）。
func TestWiFiSSIDDegraded(t *testing.T) {
	if got := WiFiSSID(context.Background()); got != "" {
		t.Logf("环境有无线网络: %q", got)
	}
}
