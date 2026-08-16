// Package service 封装 shairport-sync 服务的状态查询与控制。
// 支持 systemd（systemctl 路径自适应）与 sysvinit（service 命令）降级。
// 控制命令经 sudo -n 执行（sudoers 白名单），-n 防止权限缺失时挂起。
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrBusy 表示已有控制操作在进行中。
var ErrBusy = errors.New("另一个服务控制操作正在进行")

// ServiceStatus 是 /api/status 中 service 段的视图。
type ServiceStatus struct {
	Init      string `json:"init"` // "systemd" | "sysvinit" | "unknown"
	Active    bool   `json:"active"`
	State     string `json:"state"` // active / inactive / failed / unknown
	SubState  string `json:"sub_state"`
	Since     string `json:"since"` // 启动时间原文（本地时区格式）
	Available bool   `json:"available"`
}

// Controller 控制一个服务。
// mu 仅用于控制操作互斥（同一时刻只允许一个 start/stop/restart）；
// Detect 用 sync.Once，避免与 mu 互相嵌套导致自锁。
type Controller struct {
	mu          sync.Mutex
	detectOnce  sync.Once
	serviceName string
	sudoPath    string
	systemctl   string // 绝对路径，"" 表示不可用（sysvinit 模式）
	serviceCmd  string // sysvinit 的 service 命令绝对路径
	pidofCmd    string // pidof 绝对路径
	binaryName  string // pidof 查找的进程名
	run         func(ctx context.Context, name string, args ...string) ([]byte, int, error) // 可注入
	init        string
}

// New 创建 Controller。systemctlPath 为空则自动探测。
func New(serviceName, sudoPath, systemctlPath string) *Controller {
	c := &Controller{serviceName: serviceName, sudoPath: sudoPath, binaryName: serviceName}
	c.run = c.execCommand
	if systemctlPath != "" {
		c.systemctl = systemctlPath
	}
	return c
}

// SetRun 注入命令执行函数（测试用）。返回 stdout、exit code 与错误。
func (c *Controller) SetRun(f func(ctx context.Context, name string, args ...string) ([]byte, int, error)) {
	c.run = f
}

// execCommand 真实执行命令。exit code：0 正常；非 0 时 err 为 exitError。
func (c *Controller) execCommand(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, ee.ExitCode(), nil
		}
		return out, -1, err
	}
	return out, 0, nil
}

// Detect 探测 init 系统与命令路径（幂等，sync.Once 保证只执行一次）。
func (c *Controller) Detect() {
	c.detectOnce.Do(c.detect)
}

func (c *Controller) detect() {
	// systemctl：显式配置优先，其次固定路径，最后 PATH
	for _, p := range []string{c.systemctl, "/usr/bin/systemctl", "/bin/systemctl", ""} {
		if p == "" {
			if lp, err := exec.LookPath("systemctl"); err == nil {
				c.systemctl = lp
			}
			break
		}
		if isExecutable(p) {
			c.systemctl = p
			break
		}
	}
	if c.systemctl != "" {
		c.init = "systemd"
		return
	}
	// sysvinit fallback
	if lp, err := exec.LookPath("service"); err == nil {
		c.serviceCmd = lp
	}
	if lp, err := exec.LookPath("pidof"); err == nil {
		c.pidofCmd = lp
	}
	if c.serviceCmd != "" || c.pidofCmd != "" {
		c.init = "sysvinit"
		return
	}
	c.init = "unknown"
}

// isExecutable 判断文件存在且可执行。
func isExecutable(path string) bool {
	f, err := exec.LookPath(path)
	return err == nil && f == path
}

// Status 查询服务状态（免 sudo）。多级降级，绝不出错。
func (c *Controller) Status(ctx context.Context) ServiceStatus {
	if c.init == "" {
		c.Detect()
	}
	st := ServiceStatus{Init: c.init, State: "unknown"}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if c.init == "systemd" {
		if s, ok := c.statusSystemdShow(ctx); ok {
			return s
		}
		if s, ok := c.statusSystemdIsActive(ctx); ok {
			return s
		}
		st.Available = false
		return st
	}
	if c.init == "sysvinit" {
		if s, ok := c.statusSysv(ctx); ok {
			return s
		}
		st.Available = false
		return st
	}
	return st
}

// statusSystemdShow 首选：systemctl show（免 sudo，信息最全）。
func (c *Controller) statusSystemdShow(ctx context.Context) (ServiceStatus, bool) {
	out, code, err := c.run(ctx, c.systemctl, "show",
		"-p", "ActiveState", "-p", "SubState", "-p", "ActiveEnterTimestamp", c.serviceName)
	if err != nil || code != 0 {
		return ServiceStatus{}, false
	}
	kv := parseKeyValues(out)
	st := ServiceStatus{Init: "systemd", State: "unknown", Available: true}
	switch kv["ActiveState"] {
	case "active":
		st.Active = true
		st.State = "active"
	case "failed":
		st.State = "failed"
	case "inactive", "":
		st.State = "inactive"
	default:
		st.State = kv["ActiveState"]
	}
	st.SubState = kv["SubState"]
	st.Since = kv["ActiveEnterTimestamp"]
	return st, true
}

// statusSystemdIsActive 降级：systemctl is-active（exit 0 = active）。
func (c *Controller) statusSystemdIsActive(ctx context.Context) (ServiceStatus, bool) {
	_, code, err := c.run(ctx, c.systemctl, "is-active", c.serviceName)
	if err != nil {
		return ServiceStatus{}, false
	}
	st := ServiceStatus{Init: "systemd", Available: true}
	if code == 0 {
		st.Active, st.State = true, "active"
	} else {
		st.State = "inactive"
	}
	return st, true
}

// statusSysv sysvinit 状态：service status 退出码，降级 pidof。
func (c *Controller) statusSysv(ctx context.Context) (ServiceStatus, bool) {
	if c.serviceCmd != "" {
		_, code, err := c.run(ctx, c.serviceCmd, c.serviceName, "status")
		if err == nil {
			st := ServiceStatus{Init: "sysvinit", Available: true}
			if code == 0 {
				st.Active, st.State = true, "active"
			} else {
				st.State = "inactive"
			}
			return st, true
		}
	}
	if c.pidofCmd != "" {
		out, code, err := c.run(ctx, c.pidofCmd, c.binaryName)
		if err == nil && code == 0 && len(strings.TrimSpace(string(out))) > 0 {
			return ServiceStatus{Init: "sysvinit", Active: true, State: "active", Available: true}, true
		}
	}
	return ServiceStatus{}, false
}

// Start 启动服务。
func (c *Controller) Start(ctx context.Context) error {
	return c.control(ctx, "start", 15*time.Second)
}

// Stop 停止服务。
func (c *Controller) Stop(ctx context.Context) error {
	return c.control(ctx, "stop", 15*time.Second)
}

// Restart 重启服务。
func (c *Controller) Restart(ctx context.Context) error {
	return c.control(ctx, "restart", 30*time.Second)
}

// control 执行一个控制动作（互斥：同一时刻只允许一个）。
func (c *Controller) control(ctx context.Context, action string, timeout time.Duration) error {
	if !c.mu.TryLock() {
		return ErrBusy
	}
	defer c.mu.Unlock()

	if c.init == "" {
		c.Detect()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var name string
	var args []string
	switch c.init {
	case "systemd":
		if c.systemctl == "" {
			return errors.New("未找到 systemctl")
		}
		name = c.sudoPath
		if name == "" {
			name = c.systemctl
			args = []string{action, c.serviceName}
		} else {
			args = []string{"-n", c.systemctl, action, c.serviceName}
		}
	case "sysvinit":
		if c.serviceCmd == "" {
			return errors.New("未找到 service 命令")
		}
		name = c.sudoPath
		if name == "" {
			name = c.serviceCmd
			args = []string{c.serviceName, action}
		} else {
			args = []string{"-n", c.serviceCmd, c.serviceName, action}
		}
	default:
		return errors.New("未检测到 systemd 或 sysvinit")
	}

	out, code, err := c.run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s 失败: %v", action, err)
	}
	if code != 0 {
		return fmt.Errorf("%s 失败 (exit %d): %s", action, code, firstLine(out))
	}
	return nil
}

// parseKeyValues 解析 systemctl show 的 key=value 输出。
func parseKeyValues(out []byte) map[string]string {
	kv := make(map[string]string)
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if i := strings.IndexByte(s, '='); i > 0 {
			kv[s[:i]] = s[i+1:]
		}
	}
	return kv
}

func firstLine(out []byte) string {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
