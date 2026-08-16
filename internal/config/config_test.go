package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadSample 解析测试 fixture（真实 conf 模板拷贝）。
func loadSample(t *testing.T) *File {
	t.Helper()
	f, err := Parse("testdata/sample.conf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestParseSampleSections(t *testing.T) {
	f := loadSample(t)
	want := []string{"general", "sessioncontrol", "alsa", "metadata", "mqtt", "diagnostics"}
	for _, s := range want {
		if !f.HasSection(s) {
			t.Errorf("缺少 section %q", s)
		}
	}
}

func TestParseCommentedSettings(t *testing.T) {
	f := loadSample(t)
	for _, fk := range []string{"general.name", "general.interpolation", "alsa.output_device", "alsa.mixer_control_name", "metadata.enabled"} {
		s, ok := f.Get(strings.SplitN(fk, ".", 2)[0], strings.SplitN(fk, ".", 2)[1])
		if !ok {
			t.Errorf("未识别被注释字段 %s", fk)
			continue
		}
		if !s.Commented {
			t.Errorf("%s 应为注释状态", fk)
		}
		if s.Value != "" {
			t.Errorf("%s 注释态的 Value 应为空, 得到 %q", fk, s.Value)
		}
	}
}

// 说明文字注释（形如 // The default is ...）不能被识别为可编辑字段。
func TestParseCommentTextNotSetting(t *testing.T) {
	f := loadSample(t)
	if _, ok := f.Get("general", "The"); ok {
		t.Error("说明文字 'The' 被误认为字段")
	}
	for _, l := range f.Lines {
		if l.Kind == LineSetting && l.Key == "default" && strings.Contains(l.Raw, "The default is") {
			t.Error("说明文字被误认为字段行")
		}
	}
}

// 值中含 // 和引号的字符串必须完整解析（引号感知）。
func TestStringWithSlashAndQuote(t *testing.T) {
	content := "general =\n{\n\tname = \"客厅 // 音箱; \\\"测试\\\"\"; // 尾注释\n};\n"
	f, err := ParseBytes([]byte(content), "t")
	if err != nil {
		t.Fatal(err)
	}
	s, ok := f.Get("general", "name")
	if !ok {
		t.Fatal("name 未识别")
	}
	// Get 返回去引号的用户视图值
	if s.Value != `客厅 // 音箱; "测试"` {
		t.Errorf("Value = %q", s.Value)
	}
	if s.Commented {
		t.Error("不应为注释状态")
	}
	l := f.Settings["general.name"]
	if l.Tail != " // 尾注释" {
		t.Errorf("Tail = %q", l.Tail)
	}
}

func TestSetCommentedLine(t *testing.T) {
	f := loadSample(t)
	if err := f.Set("general", "name", `"客厅音箱"`); err != nil {
		t.Fatal(err)
	}
	l := f.Settings["general.name"]
	if l.Prefix != "" {
		t.Error("Set 后应取消注释")
	}
	if l.Value != `"客厅音箱"` {
		t.Errorf("Value = %q", l.Value)
	}
	// 尾注释保留；取消注释后的行可为顶格或缩进
	rendered := string(f.Render())
	if !strings.Contains(rendered, `name = "客厅音箱";`) {
		t.Errorf("Render 未包含新值:\n%s", rendered)
	}
	if !strings.Contains(rendered, "// This means") {
		t.Errorf("尾注释丢失:\n%s", rendered)
	}
}

func TestSetUncommentedLine(t *testing.T) {
	content := "general =\n{\n\tname = \"old\"; // 说明\n};\n"
	f, err := ParseBytes([]byte(content), "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("general", "name", `"new"`); err != nil {
		t.Fatal(err)
	}
	out := string(f.Render())
	if !strings.Contains(out, `name = "new"; // 说明`) {
		t.Errorf("尾注释未保留:\n%s", out)
	}
}

func TestSetInsertNewKey(t *testing.T) {
	content := "general =\n{\n\tname = \"x\";\n};\n\nalsa =\n{\n};\n"
	f, err := ParseBytes([]byte(content), "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("general", "password", `"s3cret"`); err != nil {
		t.Fatal(err)
	}
	out := string(f.Render())
	want := "general =\n{\n\tname = \"x\";\n\tpassword = \"s3cret\";\n};"
	if !strings.Contains(out, want) {
		t.Errorf("插入位置错误:\n%s", out)
	}
}

func TestSetAppendMissingSection(t *testing.T) {
	content := "general =\n{\n\tname = \"x\";\n};\n"
	f, err := ParseBytes([]byte(content), "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("metadata", "enabled", "yes"); err != nil {
		t.Fatal(err)
	}
	out := string(f.Render())
	if !strings.Contains(out, "metadata = {") || !strings.Contains(out, "\tenabled = yes;") {
		t.Errorf("追加 section 失败:\n%s", out)
	}
	// 追加后可再次解析
	f2, err := ParseBytes([]byte(out), "t")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := f2.Get("metadata", "enabled"); !ok || s.Commented {
		t.Errorf("二次解析 metadata.enabled 失败: %+v ok=%v", s, ok)
	}
}

func TestSetCommentedRoundTrip(t *testing.T) {
	content := "general =\n{\n\tname = \"x\";\n};\n"
	f, _ := ParseBytes([]byte(content), "t")
	if err := f.SetCommented("general", "name", true); err != nil {
		t.Fatal(err)
	}
	out := string(f.Render())
	// 注释化时值重置为 schema 默认（%H），不留自定义残留
	if !strings.Contains(out, `// name = "%H";`) {
		t.Errorf("注释化应重置为默认值:\n%s", out)
	}
	if err := f.SetCommented("general", "name", false); err != nil {
		t.Fatal(err)
	}
	out2 := string(f.Render())
	if strings.Contains(out2, "//") && strings.Contains(out2, "name") {
		t.Errorf("取消注释失败:\n%s", out2)
	}
	// 不存在的字段注释化应静默成功
	if err := f.SetCommented("general", "nonexist", true); err != nil {
		t.Errorf("不存在字段注释化应成功: %v", err)
	}
	if err := f.SetCommented("general", "nonexist", false); err == nil {
		t.Error("不存在字段取消注释应报错")
	}
}

// Render 输出再解析，字段集合与取值应一致（幂等）。
func TestRenderParseIdempotent(t *testing.T) {
	f := loadSample(t)
	if err := f.Set("general", "name", `"客厅 // 测试"`); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("alsa", "output_rate", "44100"); err != nil {
		t.Fatal(err)
	}
	rendered := f.Render()
	f2, err := ParseBytes(rendered, "t")
	if err != nil {
		t.Fatalf("二次解析失败: %v", err)
	}
	for fk := range f.Settings {
		if _, ok := f2.Settings[fk]; !ok {
			t.Errorf("二次解析丢失字段 %s", fk)
		}
	}
	s1, _ := f.Get("general", "name")
	s2, _ := f2.Get("general", "name")
	if s1.Value != s2.Value || s1.Commented != s2.Commented {
		t.Errorf("name 幂等失败: %+v vs %+v", s1, s2)
	}
}

// 修改不应影响其他行：逐行对比除目标行外的内容。
func TestRenderPreservesUntouchedLines(t *testing.T) {
	raw, _ := os.ReadFile("testdata/sample.conf")
	f, _ := ParseBytes(raw, "t")
	if err := f.Set("general", "name", `"x"`); err != nil {
		t.Fatal(err)
	}
	orig := strings.Split(string(raw), "\n")
	newOut := strings.Split(string(f.Render()), "\n")
	n := len(orig)
	if len(newOut) > n {
		n = len(newOut)
	}
	changed := 0
	for i := 0; i < n; i++ {
		o, p := "", ""
		if i < len(orig) {
			o = orig[i]
		}
		if i < len(newOut) {
			p = newOut[i]
		}
		if o != p {
			changed++
		}
	}
	// name 行 + 可能的行尾差异，正常应只有 1 行不同
	if changed != 1 {
		t.Errorf("期望仅 1 行变化, 实际 %d 行", changed)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		section, key, in string
		wantErr          bool
		wantLit          string
	}{
		{"general", "name", "客厅", false, `"客厅"`},
		{"general", "name", `a"b\c`, false, `"a\"b\\c"`},
		{"general", "name", strings.Repeat("x", 51), true, ""},
		{"general", "name", "x\ny", true, ""},
		{"general", "name", "", true, ""},
		{"general", "password", "", false, `""`},
		{"general", "volume_range_db", "120", false, "120.0"},
		{"general", "volume_range_db", "20", true, ""},
		{"general", "volume_range_db", "abc", true, ""},
		{"general", "volume_max_db", "-10.5", false, "-10.5"},
		{"general", "volume_max_db", "10", true, ""},
		{"general", "volume_control_profile", "flat", false, `"flat"`},
		{"general", "volume_control_profile", "log", true, ""},
		{"alsa", "output_format", "S24_3LE", false, `"S24_3LE"`},
		{"alsa", "output_format", "S99", true, ""},
		{"metadata", "enabled", "yes", false, `"yes"`},
		{"general", "ignore_volume_control", "yes", false, `"yes"`},
		{"general", "interpolation", "soxr", false, `"soxr"`},
		{"sessioncontrol", "active_state_timeout", "12.5", false, "12.5"},
		{"sessioncontrol", "active_state_timeout", "4000", true, ""},
	}
	for _, c := range cases {
		def, ok := ByKey(c.section, c.key)
		if !ok {
			t.Errorf("schema 缺少 %s.%s", c.section, c.key)
			continue
		}
		lit, err := def.Validate(c.in)
		if c.wantErr != (err != nil) {
			t.Errorf("%s.%s=%q: err=%v, 期望 err=%v", c.section, c.key, c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && lit != c.wantLit {
			t.Errorf("%s.%s=%q: literal=%q, 期望 %q", c.section, c.key, c.in, lit, c.wantLit)
		}
	}
}

// 回归防线：全部 schema 字段的默认值写回后必须能被解析器接受，
// 且 enum/string 字面量带引号（shairport conf 中裸 yes/no 是 libconfig 语法错误）。
func TestSchemaLiteralsParseable(t *testing.T) {
	for _, def := range Schema {
		if def.Default == "" {
			continue // 空默认值合法（如 mixer_control_name = 软件音量），Validate 会拒绝空输入
		}
		lit, err := def.Validate(def.Default)
		if err != nil {
			t.Fatalf("%s.%s 默认值校验失败: %v", def.Section, def.Key, err)
		}
		if def.Type != TypeFloat && def.Type != TypeInt && !strings.HasPrefix(lit, `"`) {
			t.Errorf("%s.%s 字面量应带引号: %q", def.Section, def.Key, lit)
		}
		// 写回的字面量必须能放进一个 section 里重新解析
		content := def.Section + " =\n{\n\t" + def.Key + " = " + lit + ";\n};\n"
		if _, err := ParseBytes([]byte(content), "t"); err != nil {
			t.Errorf("%s.%s 字面量 %s 解析失败: %v", def.Section, def.Key, lit, err)
		}
	}
}

func TestManagerApplyAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shairport-sync.conf")
	content := "general =\n{\n\tname = \"old\";\n};\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path, "", "")
	backup, err := m.ApplyChanges([]Change{
		{Section: "general", Key: "name", Value: "新名", Action: "set"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(backup, path+".bak-") {
		t.Errorf("备份名异常: %q", backup)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Errorf("备份文件不存在: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `name = "新名";`) {
		t.Errorf("写盘内容异常:\n%s", got)
	}
	// 备份内容是修改前原文
	old, _ := os.ReadFile(backup)
	if string(old) != content {
		t.Errorf("备份内容不符:\n%s", old)
	}
	// 校验失败不得写盘
	if _, err := m.ApplyChanges([]Change{
		{Section: "general", Key: "name", Value: strings.Repeat("x", 51), Action: "set"},
	}); err == nil {
		t.Error("超长 name 应报错")
	}
	got2, _ := os.ReadFile(path)
	if string(got2) != string(got) {
		t.Error("失败变更后文件被修改")
	}
}

func TestManagerReplaceRaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shairport-sync.conf")
	os.WriteFile(path, []byte("general =\n{\n};\n"), 0644)
	m := NewManager(path, "", "")
	newContent := "general =\n{\n\tname = \"raw\";\n};\n"
	if _, err := m.ReplaceRaw([]byte(newContent)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != newContent {
		t.Errorf("raw 替换不符:\n%s", got)
	}
	// 非法内容拒绝
	if _, err := m.ReplaceRaw([]byte("no general here")); err == nil {
		t.Error("无 general 的内容应被拒绝")
	}
	if _, err := m.ReplaceRaw([]byte("general = {")); err == nil {
		t.Error("括号不匹配应被拒绝")
	}
}
