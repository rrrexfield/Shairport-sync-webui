package config

import (
	"fmt"
	"strconv"
	"strings"
)

// FieldType 是配置字段的类型，决定前端控件与校验方式。
type FieldType string

const (
	TypeString FieldType = "string"
	TypeEnum   FieldType = "enum"
	TypeFloat  FieldType = "float"
	TypeInt    FieldType = "int"
)

// FieldDef 描述 shairport-sync.conf 中一个可编辑字段。
type FieldDef struct {
	Section string
	Key     string
	Type    FieldType
	Label   string   // 中文标签，前端显示
	Group   string   // "common" 常用设置 / "advanced" 高级设置
	Default string   // 被注释时的默认取值
	Enum    []string // Type=Enum 时的合法取值
	HasMin  bool
	Min     float64
	HasMax  bool
	Max     float64
	Hint    string
}

// YesNo 是布尔型枚举字段的取值。
var YesNo = []string{"no", "yes"}

// Schema 是 WebUI 可编辑字段的定义表。
// 拼写以 shairport-sync 3.3.8 的 /etc/shairport-sync.conf 为准。
var Schema = []FieldDef{
	// ---- general 段 ----
	{Section: "general", Key: "name", Type: TypeString, Label: "设备名称", Group: "common",
		Default: "%H", Hint: "AirPlay 中显示的名称，支持 %H（主机名）、%v（版本）等占位符，最长 50 字符"},
	{Section: "general", Key: "password", Type: TypeString, Label: "连接密码", Group: "common",
		Default: "", Hint: "留空表示不要求密码"},
	{Section: "general", Key: "interpolation", Type: TypeEnum, Label: "插值算法", Group: "advanced",
		Default: "auto", Enum: []string{"auto", "basic", "soxr"}},
	{Section: "general", Key: "playback_mode", Type: TypeEnum, Label: "播放模式", Group: "advanced",
		Default: "stereo", Enum: []string{"stereo", "mono", "reverse stereo", "both left", "both right"}},
	{Section: "general", Key: "ignore_volume_control", Type: TypeEnum, Label: "忽略音量控制", Group: "advanced",
		Default: "no", Enum: YesNo, Hint: "设为是则始终以 100% 音量输出"},
	{Section: "general", Key: "volume_range_db", Type: TypeFloat, Label: "音量范围 (dB)", Group: "common",
		Default: "60", HasMin: true, Min: 30, HasMax: true, Max: 150,
		Hint: "最大音量与最小音量之间的衰减范围，30 ~ 150 dB"},
	{Section: "general", Key: "volume_max_db", Type: TypeFloat, Label: "最大音量 (dB)", Group: "common",
		Default: "0.0", HasMin: true, Min: -100, HasMax: true, Max: 0,
		Hint: "硬件或软件混音器可用的最大音量，如 0.0 或 -20.0"},
	{Section: "general", Key: "volume_control_profile", Type: TypeEnum, Label: "音量调节曲线", Group: "common",
		Default: "standard", Enum: []string{"standard", "flat"},
		Hint: "standard：低音量区变化慢、高音量区变化快（听感自然）；flat：线性变化"},
	{Section: "general", Key: "regtype", Type: TypeString, Label: "服务类型", Group: "advanced",
		Default: "_raop._tcp", Hint: "Zeroconf 广播的服务类型，AirPlay 接收固定为 _raop._tcp，一般无需修改"},
	{Section: "general", Key: "dbus_service_bus", Type: TypeEnum, Label: "D-Bus 总线", Group: "advanced",
		Default: "system", Enum: []string{"system", "session"}},
	{Section: "general", Key: "mpris_service_bus", Type: TypeEnum, Label: "MPRIS 总线", Group: "advanced",
		Default: "system", Enum: []string{"system", "session"}},

	// ---- alsa 段 ----
	{Section: "alsa", Key: "output_device", Type: TypeString, Label: "音频输出设备", Group: "common",
		Default: "default", Hint: "如 default、hw:0 或声卡名称，可先到“设备”页面查看可用设备"},
	{Section: "alsa", Key: "mixer_control_name", Type: TypeString, Label: "混音器控制名", Group: "common",
		Default: "", Hint: "如 PCM、Master；留空时 shairport-sync 使用软件调音量"},
	{Section: "alsa", Key: "mixer_device", Type: TypeString, Label: "混音器设备", Group: "common",
		Default: "default"},
	{Section: "alsa", Key: "output_rate", Type: TypeEnum, Label: "输出采样率", Group: "common",
		Default: "auto", Enum: []string{"auto", "44100", "88200", "176400", "352800"}},
	{Section: "alsa", Key: "output_format", Type: TypeEnum, Label: "输出格式", Group: "common",
		Default: "auto", Enum: []string{"auto", "U8", "S8", "S16", "S16_LE", "S16_BE",
			"S24", "S24_LE", "S24_BE", "S24_3LE", "S24_3BE", "S32", "S32_LE", "S32_BE"}},
	{Section: "alsa", Key: "disable_synchronization", Type: TypeEnum, Label: "关闭同步", Group: "advanced",
		Default: "no", Enum: YesNo},

	// ---- metadata 段 ----
	{Section: "metadata", Key: "enabled", Type: TypeEnum, Label: "启用元数据管道", Group: "common",
		Default: "no", Enum: YesNo,
		Hint: "开启后本页面才能显示正在播放的歌曲与音质信息"},
	{Section: "metadata", Key: "include_cover_art", Type: TypeEnum, Label: "包含封面", Group: "advanced",
		Default: "yes", Enum: YesNo},
	{Section: "metadata", Key: "pipe_name", Type: TypeString, Label: "元数据管道路径", Group: "advanced",
		Default: "/tmp/shairport-sync-metadata"},

	// ---- sessioncontrol 段 ----
	{Section: "sessioncontrol", Key: "active_state_timeout", Type: TypeFloat, Label: "活跃状态超时 (秒)", Group: "advanced",
		Default: "10.0", HasMin: true, Min: 0, HasMax: true, Max: 3600,
		Hint: "播放结束后等待多少秒退出活跃状态"},
}

// fieldKey 返回 "section.key" 形式的字段标识。
func fieldKey(d FieldDef) string { return d.Section + "." + d.Key }

// ByKey 按 "section.key" 查找字段定义。
func ByKey(section, key string) (FieldDef, bool) {
	for _, d := range Schema {
		if d.Section == section && d.Key == key {
			return d, true
		}
	}
	return FieldDef{}, false
}

// AllDefs 返回全部字段定义（顺序同 Schema）。
func AllDefs() []FieldDef { return Schema }

// Validate 校验用户输入并转换为 libconfig 字面量（字符串带引号并转义）。
// userValue 是用户填写的原始文本，空字符串视为"未填写"错误（password 除外）。
func (d *FieldDef) Validate(userValue string) (string, error) {
	if userValue == "" && d.Key != "password" {
		return "", fmt.Errorf("不能为空")
	}
	switch d.Type {
	case TypeString:
		if strings.ContainsAny(userValue, "\n\r") {
			return "", fmt.Errorf("不允许换行")
		}
		if d.Key == "name" && len(userValue) > 50 {
			return "", fmt.Errorf("长度不能超过 50 字符")
		}
		return `"` + escapeString(userValue) + `"`, nil
	case TypeEnum:
		for _, e := range d.Enum {
			if userValue == e {
				// shairport conf 中枚举值均为带引号的字符串（如 "auto"、"yes"），
				// 裸 token 是 libconfig 语法错误（仅 true/false 合法）
				return `"` + e + `"`, nil
			}
		}
		return "", fmt.Errorf("取值必须是: %s", strings.Join(d.Enum, ", "))
	case TypeFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(userValue), 64)
		if err != nil {
			return "", fmt.Errorf("必须是数字")
		}
		if d.HasMin && f < d.Min {
			return "", fmt.Errorf("不能小于 %g", d.Min)
		}
		if d.HasMax && f > d.Max {
			return "", fmt.Errorf("不能大于 %g", d.Max)
		}
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0" // conf 要求浮点值带小数点
		}
		return s, nil
	case TypeInt:
		n, err := strconv.Atoi(strings.TrimSpace(userValue))
		if err != nil {
			return "", fmt.Errorf("必须是整数")
		}
		if d.HasMin && float64(n) < d.Min {
			return "", fmt.Errorf("不能小于 %g", d.Min)
		}
		if d.HasMax && float64(n) > d.Max {
			return "", fmt.Errorf("不能大于 %g", d.Max)
		}
		return strconv.Itoa(n), nil
	}
	return "", fmt.Errorf("未知类型 %s", d.Type)
}

// escapeString 按 libconfig 规则转义字符串内容。
func escapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}
