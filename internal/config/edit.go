package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Change 是 PUT /api/config 中的单条字段变更。
type Change struct {
	Section string `json:"section"`
	Key     string `json:"key"`
	Value   string `json:"value"`  // 用户原始输入（字符串不带引号）
	Action  string `json:"action"` // "set" 修改生效 / "default" 注释恢复默认
}

// Manager 负责配置文件的加载、修改与原子写盘。
// 写盘路径：root 直接写；非 root 经 sudo 执行白名单脚本（stdin 传内容）。
type Manager struct {
	mu          sync.Mutex
	path        string
	sudoPath    string
	writeScript string // sudo 白名单脚本绝对路径
}

// NewManager 创建 Manager。writeScript 为空表示不使用 sudo（root 直接写）。
func NewManager(path, sudoPath, writeScript string) *Manager {
	return &Manager{path: path, sudoPath: sudoPath, writeScript: writeScript}
}

// Path 返回配置文件路径。
func (m *Manager) Path() string { return m.path }

// Load 读取最新配置。
func (m *Manager) Load() (*File, error) {
	return Parse(m.path)
}

// Set 把字段设为给定 libconfig 字面量（已含引号）。
// 已有行（含被注释行）则替换并取消注释；无则插入到 section 闭合行之前；
// section 不存在则追加整个 section 块。
func (f *File) Set(section, key, literal string) error {
	if l, ok := f.Settings[section+"."+key]; ok {
		l.Value = literal
		l.Prefix = ""
		return nil
	}
	if idx, ok := f.sectionCloseIndex(section); ok {
		nl := &Line{
			Kind: LineSetting, Indent: "\t", Key: key, Value: literal,
			Stack: []string{section},
		}
		f.Lines = append(f.Lines, nil)
		copy(f.Lines[idx+1:], f.Lines[idx:])
		f.Lines[idx] = nl
		f.Settings[section+"."+key] = nl
		return nil
	}
	return f.AppendSection(section, key, literal)
}

// SetCommented 切换字段的注释状态（"使用默认值"）。
// 注释化时把值一并重置为 schema 默认字面量，使原始编辑视图中
// 被注释的行呈现模板默认值（如 // name = "%H";），不留自定义残留。
// 行不存在时：commented=true 无需创建（默认即为注释态），commented=false 报错。
func (f *File) SetCommented(section, key string, commented bool) error {
	l, ok := f.Settings[section+"."+key]
	if !ok {
		if commented {
			return nil
		}
		return fmt.Errorf("字段 %s.%s 不存在", section, key)
	}
	if commented {
		l.Prefix = "// "
		if def, ok := ByKey(section, key); ok && def.Default != "" {
			if lit, err := def.Validate(def.Default); err == nil {
				l.Value = lit
			}
		}
	} else {
		l.Prefix = ""
	}
	return nil
}

// AppendSection 在文件末尾追加一个完整 section 块（老版本 conf 缺少该 section 时）。
func (f *File) AppendSection(section, key, literal string) error {
	block := []*Line{
		{Kind: LineOther, Raw: ""},
		{Kind: LineOther, Raw: "// " + section + " settings (added by shairport-webui)"},
		{Kind: LineSectionOpen, Raw: section + " = {", Key: section},
		{Kind: LineSetting, Indent: "\t", Key: key, Value: literal, Stack: []string{section}},
		{Kind: LineSectionClose, Raw: "}"},
	}
	f.Lines = append(f.Lines, block...)
	f.Sections = append(f.Sections, section)
	f.Settings[section+"."+key] = block[3]
	return nil
}

// sectionCloseIndex 返回 section 闭合行在 Lines 中的下标。
// 基于逐行深度跟踪与块归属回溯，嵌套 section 不会混淆。
func (f *File) sectionCloseIndex(section string) (int, bool) {
	type frame struct{ name string }
	var stack []frame
	for i, l := range f.Lines {
		switch l.Kind {
		case LineSectionOpen:
			stack = append(stack, frame{l.Key})
		case LineSectionClose:
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.name == section && len(stack) == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// ApplyChanges 应用批量变更并写盘，返回备份文件名。
// 任一条变更失败则整体放弃、不写盘。
func (m *Manager) ApplyChanges(changes []Change) (backup string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := m.Load()
	if err != nil {
		return "", fmt.Errorf("读取配置失败: %w", err)
	}
	for _, c := range changes {
		def, ok := ByKey(c.Section, c.Key)
		if !ok {
			return "", fmt.Errorf("未知字段 %s.%s", c.Section, c.Key)
		}
		switch c.Action {
		case "set":
			literal, verr := def.Validate(c.Value)
			if verr != nil {
				return "", fmt.Errorf("%s（%s）: %v", def.Label, def.Key, verr)
			}
			if err := f.Set(c.Section, c.Key, literal); err != nil {
				return "", err
			}
		case "default":
			if err := f.SetCommented(c.Section, c.Key, true); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("未知操作 %q（%s.%s）", c.Action, c.Section, c.Key)
		}
	}
	if err := f.sanityCheck(); err != nil {
		return "", fmt.Errorf("自检失败: %v", err)
	}
	return m.writeContent(f.Render())
}

// ResetToDefaults 把 schema 管理的全部字段恢复为注释态（默认值），
// 其余内容（说明注释、非 schema 字段）原样保留。返回备份文件名。
func (m *Manager) ResetToDefaults() (backup string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, err := m.Load()
	if err != nil {
		return "", fmt.Errorf("读取配置失败: %w", err)
	}
	for _, def := range Schema {
		if err := f.SetCommented(def.Section, def.Key, true); err != nil {
			return "", err
		}
	}
	if err := f.sanityCheck(); err != nil {
		return "", fmt.Errorf("自检失败: %v", err)
	}
	return m.writeContent(f.Render())
}

// ReplaceRaw 整体替换配置内容（高级编辑），返回备份文件名。
func (m *Manager) ReplaceRaw(content []byte) (backup string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(content) == 0 || len(content) > 64*1024 {
		return "", fmt.Errorf("内容为空或超过 64KB")
	}
	f, err := ParseBytes(content, m.path)
	if err != nil {
		return "", fmt.Errorf("解析失败: %w", err)
	}
	if !f.HasSection("general") {
		return "", fmt.Errorf("内容中找不到 general 段")
	}
	if err := f.sanityCheck(); err != nil {
		return "", fmt.Errorf("自检失败: %v", err)
	}
	return m.writeContent(content)
}

// writeContent 把内容写盘：root 直接原子写，否则 sudo 白名单脚本。
// 返回备份文件名。
func (m *Manager) writeContent(content []byte) (string, error) {
	if os.Geteuid() == 0 {
		ts := time.Now().Format("20060102150405")
		backup := m.path + ".bak-" + ts
		if old, err := os.ReadFile(m.path); err == nil {
			if err := os.WriteFile(backup, old, 0644); err != nil {
				return "", fmt.Errorf("备份失败: %w", err)
			}
		}
		tmp := m.path + ".tmp"
		if err := os.WriteFile(tmp, content, 0644); err != nil {
			return "", fmt.Errorf("写临时文件失败: %w", err)
		}
		if err := os.Rename(tmp, m.path); err != nil {
			os.Remove(tmp)
			return "", fmt.Errorf("替换失败: %w", err)
		}
		return backup, nil
	}
	if m.sudoPath == "" || m.writeScript == "" {
		return "", fmt.Errorf("非 root 且未配置 sudo 写盘脚本")
	}
	cmd := exec.Command(m.sudoPath, "-n", m.writeScript)
	cmd.Stdin = strings.NewReader(string(content))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sudo 写盘失败: %v: %s", err, strings.TrimSpace(string(out)))
	}
	backup := strings.TrimSpace(string(out))
	if backup == "" || !strings.Contains(backup, ".bak-") {
		return "", fmt.Errorf("写盘脚本返回异常: %q", string(out))
	}
	return backup, nil
}
