package config

import (
	"fmt"
	"os"
	"strings"
)

// LineKind 标识一行在 libconfig 结构中的角色。
type LineKind int

const (
	LineOther         LineKind = iota // 空行、纯注释、说明文字、数组等，永不修改
	LineSectionOpen                   // "general = {"
	LineSectionClose                  // "}"
	LineSetting                       // "key = value;"（含被 // 注释的形式）
)

// Line 是解析后的一行。Setting 行按结构拆分以便无损改写，
// 其余行保留原文 Raw 永不修改。
type Line struct {
	No     int
	Raw    string // 原样行文本（LineOther/SectionOpen/SectionClose 用；Setting 行由字段重组）
	Kind   LineKind
	Indent string   // 行首空白
	Prefix string   // 注释前缀（"" 未注释；"//\t"、"// " 等原样）
	Key    string   // 仅 LineSetting
	Value  string   // '=' 与 ';' 之间的原文（含引号），仅 LineSetting
	Tail   string   // 行尾注释原文（从 // 或 # 起到行尾，含前导空格），仅 LineSetting
	Stack  []string // 该行所处的 section 栈（LineSetting 归属栈顶 section）
}

// File 是解析后的整个配置文件。
type File struct {
	Path     string
	Lines    []*Line
	Settings map[string]*Line // "section.key" -> 行指针（同 key 重复出现时保留第一个）
	Sections []string         // section 出现顺序
}

// Setting 是面向外部的字段视图。
type Setting struct {
	Section   string `json:"section"`
	Key       string `json:"key"`
	Value     string `json:"value"`     // 原文（含引号），被注释时为空字符串
	Commented bool   `json:"commented"` // 整行被注释（= 使用默认值）
	LineNo    int    `json:"line_no"`
}

// Parse 读取并解析配置文件。
func Parse(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(b, path)
}

// ParseBytes 解析内存中的配置内容，path 仅用于标识。
// 兼容两种 section 开形式：单行 "general = {" 与两行 "general =" 后跟 "{"
//（真实 conf 模板为两行式）。
func ParseBytes(b []byte, path string) (*File, error) {
	f := &File{Path: path, Settings: make(map[string]*Line)}
	var stack []string
	pendingOpen := "" // 上一行是 "key =" 形式、等待下一行 "{" 确认
	for i, raw := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		// TrimRight 去掉文件末尾换行，避免产生虚假空行
		line := parseLine(raw, i+1, stack)

		// 两行式 section 开：确认 "{" 行，把上一行改为 LineSectionOpen
		if pendingOpen != "" {
			if isBraceLine(raw) {
				prev := f.Lines[len(f.Lines)-1]
				prev.Kind = LineSectionOpen
				prev.Key = pendingOpen
				stack = append(stack, pendingOpen)
				f.Sections = append(f.Sections, pendingOpen)
				pendingOpen = ""
				f.Lines = append(f.Lines, line) // "{" 行保持 LineOther
				continue
			}
			pendingOpen = "" // 未跟 "{"，放弃待定
		}
		if line.Kind == LineOther {
			if k, ok := sectionOpenOnlyKey(raw); ok {
				pendingOpen = k
			}
		}

		switch line.Kind {
		case LineSectionOpen:
			stack = append(stack, line.Key)
			f.Sections = append(f.Sections, line.Key)
		case LineSectionClose:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case LineSetting:
			if len(stack) == 0 {
				line.Kind = LineOther // 顶层游离的 key = value; 不归属任何 section，永不修改
				line.Raw = raw
				break
			}
			line.Stack = append([]string(nil), stack...)
			fk := stack[len(stack)-1] + "." + line.Key
			if _, dup := f.Settings[fk]; !dup {
				f.Settings[fk] = line
			}
		}
		f.Lines = append(f.Lines, line)
	}
	return f, nil
}

// isBraceLine 判断行是否为纯 "{" 或 "{" 带尾注释。
func isBraceLine(raw string) bool {
	code, _ := stripTailComment(strings.TrimSpace(raw))
	return strings.HasPrefix(code, "{")
}

// sectionOpenOnlyKey 判断行是否为 "key =" 形式（无值，等待下一行 "{"），返回 section 名。
func sectionOpenOnlyKey(raw string) (string, bool) {
	code, _ := stripTailComment(strings.TrimSpace(raw))
	if strings.HasSuffix(code, "{") {
		return "", false // 单行式由 parseLine 处理
	}
	eq := strings.Index(code, "=")
	if eq <= 0 || strings.TrimSpace(code[eq+1:]) != "" {
		return "", false
	}
	k := strings.TrimSpace(code[:eq])
	if !validKey(k) {
		return "", false
	}
	return k, true
}

// parseLine 解析单行。stack 是进入该行前的 section 栈。
func parseLine(raw string, no int, stack []string) *Line {
	line := &Line{No: no, Raw: raw, Kind: LineOther}
	line.Indent = raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return line
	}

	// 被注释的 setting："// key = value;"（必须先于尾注释剥离判断，否则 // 开头整行被当注释）
	if strings.HasPrefix(trimmed, "//") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
		if code2, tail2 := stripTailComment(rest); isSettingShape(code2) {
			// 注释前缀原文：从 "//" 起到 key 之前（如 "//\t"、"// "）
			prefix := trimmed[:len(trimmed)-len(rest)]
			return parseSetting(line, code2, prefix, tail2)
		}
		return line // 说明文字注释
	}

	// 引号感知剥离行尾注释，得到 code 与 tail
	code, tail := stripTailComment(trimmed)

	if code == "}" || code == "};" {
		line.Kind = LineSectionClose
		return line
	}

	// section 开："name = {" 或带尾注释
	if k, ok := sectionOpenKey(code); ok {
		line.Kind = LineSectionOpen
		line.Key = k
		return line
	}

	if isSettingShape(code) {
		return parseSetting(line, code, "", tail)
	}
	return line
}

// stripTailComment 在引号外寻找 // 或 #，返回注释前的代码与注释原文。
// libconfig 字符串内（"" 之间）的 // 与 # 不算注释。
// tail 回溯包含注释前的前导空白（如 "; // 说明" 返回 " // 说明"），保证写回保真。
func stripTailComment(s string) (code, tail string) {
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			if i == 0 || s[i-1] != '\\' {
				inQuote = !inQuote
			}
		case '/':
			if !inQuote && i+1 < len(s) && s[i+1] == '/' {
				return s[:wsBack(s, i)], s[wsBack(s, i):]
			}
		case '#':
			if !inQuote {
				return s[:wsBack(s, i)], s[wsBack(s, i):]
			}
		}
	}
	return s, ""
}

// wsBack 从 i 向前回溯连续空白，返回空白区起点。
func wsBack(s string, i int) int {
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}
	return i
}

// isSettingShape 判断 code 是否为 "key = value;" 形状。
// 与 parseSetting 共享同一解析入口，保证两处行为一致。
func isSettingShape(code string) bool {
	_, _, ok := splitSetting(code)
	return ok
}

// splitSetting 解析 "key = value;"：返回 key、value 原文与是否成功。
// '=' 与结尾 ';' 都必须位于引号外。
func splitSetting(code string) (key, value string, ok bool) {
	eq := -1
	inQuote := false
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '"':
			if i == 0 || code[i-1] != '\\' {
				inQuote = !inQuote
			}
		case '=':
			if !inQuote {
				eq = i
				goto found
			}
		}
	}
found:
	if eq <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(code[:eq])
	if !validKey(key) {
		return "", "", false
	}
	// 引号外最后一个 ';' 作为值的结束
	rest := code[eq+1:]
	lastSemi := -1
	inQuote = false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '"':
			if i == 0 || rest[i-1] != '\\' {
				inQuote = !inQuote
			}
		case ';':
			if !inQuote {
				lastSemi = i
			}
		}
	}
	if lastSemi < 0 {
		return "", "", false
	}
	// 只去左端空白，保留值尾空白（如 "60 ;" 原文），写回保真
	value = strings.TrimLeft(rest[:lastSemi], " \t")
	if strings.TrimSpace(value) == "" {
		return "", "", false
	}
	return key, value, true
}

// validKey 校验 key 为合法标识符。
func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// sectionOpenKey 判断 code 是否为 "name = {" 形状并返回 section 名。
func sectionOpenKey(code string) (string, bool) {
	eq := -1
	inQuote := false
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '"':
			if i == 0 || code[i-1] != '\\' {
				inQuote = !inQuote
			}
		case '=':
			if !inQuote {
				eq = i
				goto found
			}
		}
	}
found:
	if eq <= 0 {
		return "", false
	}
	key := strings.TrimSpace(code[:eq])
	if !validKey(key) {
		return "", false
	}
	rest := strings.TrimSpace(code[eq+1:])
	if !strings.HasPrefix(rest, "{") {
		return "", false
	}
	return key, true
}

// parseSetting 填充一个 LineSetting 行。
// prefix 是注释前缀原文（如 "//\t" 或 ""），tail 是行尾注释原文。
func parseSetting(line *Line, code, prefix, tail string) *Line {
	key, value, ok := splitSetting(code)
	if !ok {
		return line
	}
	line.Kind = LineSetting
	line.Prefix = prefix
	line.Key = key
	line.Value = value
	line.Tail = tail
	return line
}

// Render 把解析结果重组为文件内容，未修改的行逐字节保真。
func (f *File) Render() []byte {
	var b strings.Builder
	for i, l := range f.Lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if l.Kind == LineSetting {
			b.WriteString(l.Indent)
			b.WriteString(l.Prefix)
			b.WriteString(l.Key)
			b.WriteString(" = ")
			b.WriteString(l.Value)
			b.WriteByte(';')
			b.WriteString(l.Tail)
		} else {
			b.WriteString(l.Raw)
		}
	}
	b.WriteByte('\n')
	return []byte(b.String())
}

// Get 返回 "section.key" 的当前视图。key 不存在返回 ok=false。
// Value 为去引号的用户视图值（"yes" → yes），内部行编辑仍保留原文。
func (f *File) Get(section, key string) (Setting, bool) {
	l, ok := f.Settings[section+"."+key]
	if !ok {
		return Setting{}, false
	}
	s := Setting{Section: section, Key: key, Value: strings.TrimSpace(l.Value), Commented: l.Prefix != "", LineNo: l.No}
	if l.Prefix != "" {
		s.Value = ""
	} else {
		s.Value = unquote(s.Value)
	}
	return s, true
}

// unquote 去掉 libconfig 字符串字面量的外层双引号并反转义（非字符串原样返回）。
// 先处理 \" 再处理 \\，保证 "\\\""（转义反斜杠+引号）语义正确。
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		inner = strings.ReplaceAll(inner, `\\`, `\`)
		return inner
	}
	return s
}

// SettingsIn 返回某 section 的全部已知字段（含被注释行）。
func (f *File) SettingsIn(section string) []Setting {
	var out []Setting
	for _, l := range f.Lines {
		if l.Kind != LineSetting || len(l.Stack) == 0 || l.Stack[len(l.Stack)-1] != section {
			continue
		}
		s, _ := f.Get(section, l.Key)
		out = append(out, s)
	}
	return out
}

// HasSection 判断 section 是否存在。
func (f *File) HasSection(section string) bool {
	for _, s := range f.Sections {
		if s == section {
			return true
		}
	}
	return false
}

// 校验：括号平衡、引号闭合，用于写盘前的轻量自检。
// 忽略字符串内与 // 行注释内的字符（注释文本里常见 { } 示例）。
func (f *File) sanityCheck() error {
	content := f.Render()
	open, close := 0, 0
	inQuote, inComment := false, false
	for i := 0; i < len(content); i++ {
		ch := content[i]
		if inComment {
			if ch == '\n' {
				inComment = false
			}
			continue
		}
		if inQuote {
			if ch == '"' && content[i-1] != '\\' {
				inQuote = false
			}
			continue
		}
		switch ch {
		case '"':
			inQuote = true
		case '/':
			if i+1 < len(content) && content[i+1] == '/' {
				inComment = true
				i++
			}
		case '{':
			open++
		case '}':
			close++
		}
	}
	if open != close {
		return fmt.Errorf("括号不匹配: %d 个 { vs %d 个 }", open, close)
	}
	if inQuote {
		return fmt.Errorf("引号未闭合")
	}
	return nil
}
