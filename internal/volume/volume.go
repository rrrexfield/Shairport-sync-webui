// Package volume 提供 ALSA 混音器信息查询。
// 音量调节已移除（用户通过手机 AirPlay 音量调节，由 shairport-sync 软件音量处理），
// 仅保留控件枚举供配置页选择 mixer_control_name 时参考。
package volume

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// ErrNotInstalled 表示系统中不存在 amixer。
var ErrNotInstalled = errors.New("amixer 未安装")

// ListControls 枚举混音器控制名（amixer scontrols）。
// 每行形如 "Simple mixer control 'Master',0"。
func ListControls(ctx context.Context, card string) ([]string, error) {
	path, err := exec.LookPath("amixer")
	if err != nil {
		return nil, ErrNotInstalled
	}
	args := []string{"-M"}
	if card != "" {
		args = append(args, "-c", card)
	}
	args = append(args, "scontrols")
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return nil, err
	}
	return parseControlNames(string(out)), nil
}

// parseControlNames 解析 "Simple mixer control 'Master',0" 行中的控制名。
func parseControlNames(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "'"); i >= 0 {
			rest := line[i+1:]
			if j := strings.Index(rest, "'"); j > 0 {
				names = append(names, rest[:j])
			}
		}
	}
	return names
}
