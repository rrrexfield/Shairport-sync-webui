// Package webuiconf 加载 WebUI 自身配置（/etc/shairport-webui.conf）。
// 优先级：默认值 ← JSON 文件 ← 命令行 flag（由 main 覆盖）。
package webuiconf

import (
	"encoding/json"
	"os"
)

// Config 是 WebUI 运行时配置。
type Config struct {
	Listen           string `json:"listen"`            // HTTP 监听地址，默认 ":8080"
	ShairportService string `json:"shairport_service"` // 服务名，默认 "shairport-sync"
	ShairportConf    string `json:"shairport_conf"`    // 配置文件路径
	MetadataPipe     string `json:"metadata_pipe"`     // 元数据管道路径，空 = 从 conf 解析
	MixerControl     string `json:"mixer_control"`     // 空 = 按优先级探测
	MixerCard        string `json:"mixer_card"`        // 空 = 默认卡
	MixerDevice      string `json:"mixer_device"`      // 空 = 默认设备
	SudoPath         string `json:"sudo_path"`         // 默认 "/usr/bin/sudo"
	SystemctlPath    string `json:"systemctl_path"`    // 空 = 自动探测
	WriteScript      string `json:"write_script"`      // sudo 白名单写配置脚本
	DbusAddress      string `json:"dbus_address"`      // 调试用，空 = 系统总线
}

// Default 返回默认配置。
func Default() *Config {
	return &Config{
		Listen:           ":8080",
		ShairportService: "shairport-sync",
		ShairportConf:    "/etc/shairport-sync.conf",
		MetadataPipe:     "",
		MixerControl:     "",
		SudoPath:         "/usr/bin/sudo",
		SystemctlPath:    "",
		WriteScript:      "/usr/libexec/shairport-webui/write-config.sh",
	}
}

// Load 读取配置文件，与默认值合并（未出现的字段保留默认值）。
// 文件不存在时静默返回默认配置；解析失败返回错误（调用方告警但不阻止启动）。
func Load(path string) (*Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
