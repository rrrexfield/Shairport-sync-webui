// Package app 聚合各数据源，向 API 层提供统一视图。
// 每个数据源独立降级：单源故障绝不影响其他源与整体响应。
package app

import (
	"context"
	"sync"
	"time"

	"shairport-webui/internal/config"
	"shairport-webui/internal/dbusx"
	"shairport-webui/internal/pipe"
	"shairport-webui/internal/service"
	"shairport-webui/internal/sysinfo"
	"shairport-webui/internal/webuiconf"
)

// App 持有全部子模块。
type App struct {
	Cfg     *webuiconf.Config
	Service *service.Controller
	Config  *config.Manager
	Pipe    *pipe.Reader
	Dbus    *dbusx.Client
	Version string // WebUI 自身版本（构建时注入）
}

// New 装配 App。
func New(cfg *webuiconf.Config) *App {
	return &App{
		Cfg:     cfg,
		Service: service.New(cfg.ShairportService, cfg.SudoPath, cfg.SystemctlPath),
		Config:  config.NewManager(cfg.ShairportConf, cfg.SudoPath, cfg.WriteScript),
		Pipe:    pipe.New(cfg.MetadataPipe),
		Dbus:    dbusx.New(cfg.DbusAddress),
	}
}

// StatusPayload 是 /api/status 的响应体。
type StatusPayload struct {
	TS           int64                 `json:"ts"`
	WebuiVersion string                `json:"webui_version"`
	Service      service.ServiceStatus `json:"service"`
	Player       dbusx.PlayerStatus    `json:"player"`
	Track        pipe.Snapshot         `json:"track"`
	MetaPipe     pipe.State            `json:"meta_pipe"`
	Sys          sysinfo.Sys           `json:"sys"`
}

// Status 并发聚合各数据源状态。总 deadline 4s，各源独立超时。
func (a *App) Status(ctx context.Context) StatusPayload {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	p := StatusPayload{TS: time.Now().Unix(), WebuiVersion: a.Version}
	p.Sys = sysinfo.Get()

	var wg sync.WaitGroup
	wg.Add(5)

	go func() { // service 5s
		defer wg.Done()
		sub, c := context.WithTimeout(ctx, 5*time.Second)
		defer c()
		p.Service = a.Service.Status(sub)
	}()
	go func() { // wifi ssid 2s（探测命令可能较慢，独立执行；写不同字段，无竞争）
		defer wg.Done()
		sub, c := context.WithTimeout(ctx, 2*time.Second)
		defer c()
		p.Sys.WiFiSSID = sysinfo.WiFiSSID(sub)
	}()
	go func() { // dbus 1.5s
		defer wg.Done()
		sub, c := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer c()
		done := make(chan struct{})
		var ps dbusx.PlayerStatus
		go func() {
			ps = a.Dbus.GetPlayerStatus()
			close(done)
		}()
		select {
		case <-done:
			p.Player = ps
		case <-sub.Done():
			p.Player = dbusx.PlayerStatus{} // 超时零值
		}
	}()
	go func() { // pipe 快照（纯内存，立即）
		defer wg.Done()
		p.Track = a.Pipe.Snapshot()
	}()
	go func() { // pipe 状态（纯内存，立即）
		defer wg.Done()
		p.MetaPipe = a.Pipe.State()
	}()

	wg.Wait()
	return p
}
