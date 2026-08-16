// Package sysinfo 读取主机基础信息（/proc 与网络探测），供状态页展示。
// 300MB 内存的目标设备上展示内存占用有实际意义。
package sysinfo

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sys 是主机信息快照。
type Sys struct {
	Hostname    string  `json:"hostname"`
	MemTotalKB  int64   `json:"mem_total_kb"`
	MemAvailKB  int64   `json:"mem_avail_kb"`
	Load1       float64 `json:"load1"`
	LoadPercent int     `json:"load_percent"` // load1 / CPU 核数 × 100，可超过 100（过载）
	WiFiSSID    string  `json:"wifi_ssid"`
}

// Get 读取主机信息。任何读取失败均返回零值，不报错。
func Get() Sys {
	s := Sys{}
	if hn, err := os.Hostname(); err == nil {
		s.Hostname = hn
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		s.MemTotalKB, s.MemAvailKB = parseMeminfo(string(b))
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64); err == nil {
			s.Load1 = f
			if cores := cpuCount(); cores > 0 {
				s.LoadPercent = int(f / float64(cores) * 100)
			}
		}
	}
	return s
}

// cpuCount 统计 /proc/cpuinfo 的处理器数（真实核数），失败回退到 0。
func cpuCount() int {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\nprocessor")
}

// WiFiSSID 返回当前连接的无线网络名称（SSID），未连接/有线返回 ""。
// 探测链：nmcli → iwgetid → iw；结果缓存 30s，避免每次轮询都 exec。
var (
	ssidMu    sync.Mutex
	ssidCache string
	ssidAt    time.Time
)

func WiFiSSID(ctx context.Context) string {
	ssidMu.Lock()
	if time.Since(ssidAt) < 30*time.Second {
		v := ssidCache
		ssidMu.Unlock()
		return v
	}
	ssidMu.Unlock()

	ssid := probeSSID(ctx)

	ssidMu.Lock()
	ssidCache = ssid
	ssidAt = time.Now()
	ssidMu.Unlock()
	return ssid
}

func probeSSID(ctx context.Context) string {
	// 1) NetworkManager
	if out, err := exec.CommandContext(ctx, "nmcli", "-t", "-f", "active,ssid", "dev", "wifi").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "yes:") {
				if ssid := unescapeSSID(strings.TrimPrefix(line, "yes:")); ssid != "" && ssid != "--" {
					return ssid
				}
			}
		}
	}
	// 2) wireless-tools
	if out, err := exec.CommandContext(ctx, "iwgetid", "-r").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	// 3) iw：取第一个无线接口的 SSID
	if out, err := exec.CommandContext(ctx, "iw", "dev").Output(); err == nil {
		iface := ""
		for _, line := range strings.Split(string(out), "\n") {
			if i := strings.Index(line, "Interface"); i >= 0 {
				iface = strings.TrimSpace(line[i+len("Interface"):])
				break
			}
		}
		if iface != "" {
			if out2, err := exec.CommandContext(ctx, "iw", "dev", iface, "link").Output(); err == nil {
				for _, line := range strings.Split(string(out2), "\n") {
					if i := strings.Index(line, "SSID:"); i >= 0 {
						return strings.TrimSpace(line[i+len("SSID:"):])
					}
				}
			}
		}
	}
	return ""
}

// unescapeSSID 反转义 nmcli 输出（\: \\ \s）。
func unescapeSSID(s string) string {
	s = strings.ReplaceAll(s, `\:`, `:`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\s`, ` `)
	return strings.TrimSpace(s)
}

// parseMeminfo 提取 MemTotal 与 MemAvailable（老内核无 MemAvailable 时用 MemFree 近似）。
func parseMeminfo(content string) (total, avail int64) {
	free := int64(-1)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = n
		case "MemAvailable":
			avail = n
		case "MemFree":
			free = n
		}
	}
	if avail == 0 && free >= 0 {
		avail = free // 老内核近似
	}
	return total, avail
}
