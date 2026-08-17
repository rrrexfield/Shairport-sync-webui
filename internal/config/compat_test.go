package config

import (
	"os"
	"strings"
	"testing"
)

// 用真实 5.2.1 conf（交付包模板）验证「打开开关 → 注释被删除」的写盘链路。
// 5.2.1 模板的注释行顶格（"//	name = ..."），Set 后应渲染为顶格生效行。
func TestRepro521Set(t *testing.T) {
	raw, err := os.ReadFile("../../dist-arm64/shairport-sync-arm64/etc/shairport-sync.conf")
	if err != nil {
		t.Skip("无 5.2.1 conf 样本:", err)
	}
	f, err := ParseBytes(raw, "t")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, fk := range []string{"general.name", "general.ignore_volume_control", "general.playback_mode", "metadata.enabled", "alsa.output_device"} {
		parts := strings.SplitN(fk, ".", 2)
		s, ok := f.Get(parts[0], parts[1])
		if !ok {
			t.Errorf("5.2.1 conf 中 %s 未识别", fk)
			continue
		}
		if !s.Commented {
			t.Errorf("%s 应为注释态", fk)
		}
	}
	if err := f.Set("metadata", "enabled", `"yes"`); err != nil {
		t.Fatalf("Set metadata.enabled: %v", err)
	}
	if err := f.Set("general", "ignore_volume_control", `"yes"`); err != nil {
		t.Fatalf("Set ignore_volume_control: %v", err)
	}
	out := string(f.Render())
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "enabled = \"yes\"") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("metadata.enabled 注释未删除: %q", line)
			}
		}
	}
	// 顶格生效行（5.2.1 模板注释行顶格，Set 后保留该缩进）
	if !strings.Contains(out, "enabled = \"yes\"; // set this to yes") {
		t.Errorf("未找到生效行:\n%s", out)
	}
	if strings.Contains(out, `//	ignore_volume_control = "yes";`) {
		t.Error("ignore_volume_control 注释未删除")
	}
	// 二次解析验证
	f2, err := ParseBytes([]byte(out), "t")
	if err != nil {
		t.Fatalf("二次解析: %v", err)
	}
	if s, ok := f2.Get("metadata", "enabled"); !ok || s.Commented || s.Value != "yes" {
		t.Errorf("二次解析 metadata.enabled: ok=%v commented=%v value=%q", ok, s.Commented, s.Value)
	}
}
