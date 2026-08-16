package service

import (
	"context"
	"strings"
	"testing"
)

// fakeRunner 记录调用并返回预设结果。
type fakeRunner struct {
	calls  []string // "name arg1 arg2 ..."
	outs   [][]byte
	codes  []int
	errs   []error
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	i := len(f.calls) - 1
	var out []byte
	var code int
	var err error
	if i < len(f.outs) {
		out = f.outs[i]
	}
	if i < len(f.codes) {
		code = f.codes[i]
	}
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return out, code, err
}

func (f *fakeRunner) last() string {
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func TestStatusSystemdShow(t *testing.T) {
	c := New("shairport-sync", "/usr/bin/sudo", "/usr/bin/systemctl")
	f := &fakeRunner{}
	c.SetRun(f.run)
	f.outs = [][]byte{[]byte("ActiveState=active\nSubState=running\nActiveEnterTimestamp=Sun 2026-08-16 13:07:11 CST\n")}
	f.codes = []int{0}

	st := c.Status(context.Background())
	if !st.Active || st.State != "active" || st.SubState != "running" {
		t.Errorf("Status = %+v", st)
	}
	if st.Since != "Sun 2026-08-16 13:07:11 CST" {
		t.Errorf("Since = %q", st.Since)
	}
	if !strings.Contains(f.last(), "show -p ActiveState") {
		t.Errorf("命令不符: %q", f.last())
	}
}

func TestStatusSystemdFallbackIsActive(t *testing.T) {
	c := New("shairport-sync", "/usr/bin/sudo", "/usr/bin/systemctl")
	f := &fakeRunner{}
	c.SetRun(f.run)
	// show 失败（exit 1）→ 降级 is-active（exit 3 = inactive）
	f.outs = [][]byte{nil, nil}
	f.codes = []int{1, 3}

	st := c.Status(context.Background())
	if st.Active || st.State != "inactive" || !st.Available {
		t.Errorf("Status = %+v", st)
	}
	if !strings.Contains(f.last(), "is-active") {
		t.Errorf("未降级 is-active: %q", f.last())
	}
}

func TestStatusSysvinit(t *testing.T) {
	c := New("shairport-sync", "/usr/bin/sudo", "")
	// 直接设置字段构造 sysvinit 场景（绕过 Detect 的 once）
	c.init = "sysvinit"
	c.serviceCmd = "/usr/sbin/service"
	c.pidofCmd = "/bin/pidof"

	f := &fakeRunner{}
	c.SetRun(f.run)
	f.outs = [][]byte{[]byte("shairport-sync is running")}
	f.codes = []int{0}

	st := c.Status(context.Background())
	if !st.Active || st.State != "active" || st.Init != "sysvinit" {
		t.Errorf("Status = %+v", st)
	}
	if !strings.Contains(f.last(), "/usr/sbin/service shairport-sync status") {
		t.Errorf("命令不符: %q", f.last())
	}
}

func TestStatusSysvFallbackPidof(t *testing.T) {
	c := New("shairport-sync", "/usr/bin/sudo", "")
	c.init = "sysvinit"
	c.serviceCmd = ""
	c.pidofCmd = "/bin/pidof"

	f := &fakeRunner{}
	c.SetRun(f.run)
	f.outs = [][]byte{[]byte("1675\n")}
	f.codes = []int{0}

	st := c.Status(context.Background())
	if !st.Active || !st.Available {
		t.Errorf("Status = %+v", st)
	}
}

func TestControlSystemd(t *testing.T) {
	c := New("shairport-sync", "/usr/bin/sudo", "/usr/bin/systemctl")
	f := &fakeRunner{}
	c.SetRun(f.run)
	f.codes = []int{0}
	f.errs = []error{nil}

	if err := c.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "/usr/bin/sudo -n /usr/bin/systemctl restart shairport-sync"
	if f.last() != want {
		t.Errorf("命令 = %q, 期望 %q", f.last(), want)
	}
}

func TestControlExitError(t *testing.T) {
	c := New("shairport-sync", "/usr/bin/sudo", "/usr/bin/systemctl")
	f := &fakeRunner{}
	c.SetRun(f.run)
	f.outs = [][]byte{[]byte("failed to start")}
	f.codes = []int{5}

	err := c.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exit 5") {
		t.Errorf("err = %v", err)
	}
}

func TestControlNoSudoDirect(t *testing.T) {
	c := New("shairport-sync", "", "/usr/bin/systemctl")
	f := &fakeRunner{}
	c.SetRun(f.run)
	f.codes = []int{0}

	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "/usr/bin/systemctl stop shairport-sync"
	if f.last() != want {
		t.Errorf("命令 = %q, 期望 %q", f.last(), want)
	}
}

func TestControlBusy(t *testing.T) {
	c := New("shairport-sync", "/usr/bin/sudo", "/usr/bin/systemctl")
	// 先占住互斥锁
	c.mu.Lock()
	err := c.Restart(context.Background())
	c.mu.Unlock()
	if err != ErrBusy {
		t.Errorf("err = %v, 期望 ErrBusy", err)
	}
}
