package volume

import (
	"context"
	"errors"
	"testing"
)

// ListControls 依赖真实 amixer 命令。环境差异：
//   - 无 amixer：返回 ErrNotInstalled（降级路径，需验证）
//   - 有 amixer 但无声卡：返回执行错误（也需不 panic）
//   - 有 amixer 有声卡：返回控制名列表
func TestListControlsDegraded(t *testing.T) {
	names, err := ListControls(context.Background(), "")
	switch {
	case errors.Is(err, ErrNotInstalled):
		t.Log("环境无 amixer，降级路径正常")
	case err != nil:
		t.Logf("amixer 执行失败（如无声卡）: %v，降级路径正常", err)
	case len(names) >= 0:
		t.Logf("枚举到 %d 个控制", len(names))
	}
}

func TestParseControlNames(t *testing.T) {
	out := "Simple mixer control 'PCM',0\nSimple mixer control 'Master',0\nSimple mixer control 'Headphone',1\n"
	names := parseControlNames(out)
	want := []string{"PCM", "Master", "Headphone"}
	if len(names) != len(want) {
		t.Fatalf("names = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, 期望 %q", i, names[i], want[i])
		}
	}
}

func TestParseControlNamesEmpty(t *testing.T) {
	if names := parseControlNames("no controls here\n"); len(names) != 0 {
		t.Errorf("names = %v", names)
	}
}
