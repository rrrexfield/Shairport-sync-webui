// shairport-webui：Shairport Sync 的轻量 Web 管理界面。
// 目标：Ubuntu/Debian（含骁龙410 + 300MB RAM 的嵌入式设备），
// 单二进制 + 内嵌前端，内存占用 < 10MB。
package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shairport-webui/internal/api"
	"shairport-webui/internal/app"
	"shairport-webui/internal/config"
	"shairport-webui/internal/webuiconf"
)

//go:embed static
var staticFS embed.FS

// version 由构建注入：go build -ldflags "-X main.version=1.0.0"
var version = "dev"

func main() {
	var confPath string
	flag.StringVar(&confPath, "conf", "/etc/shairport-webui.conf", "WebUI 配置文件路径")
	flag.StringVar(&confPath, "c", "/etc/shairport-webui.conf", "WebUI 配置文件路径（简写）")
	flag.Parse()

	cfg, err := webuiconf.Load(confPath)
	if err != nil {
		log.Printf("警告: 读取配置 %s 失败, 使用默认值: %v", confPath, err)
	}

	// 未显式配置 pipe 路径时从 shairport conf 解析
	if cfg.MetadataPipe == "" {
		cfg.MetadataPipe = defaultPipePath(cfg)
	}

	a := app.New(cfg)
	a.Version = version

	// metadata pipe 常驻读取
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Pipe.Run(ctx)

	// 静态资源
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("加载前端资源失败: %v", err)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.NewServer(a, http.FileServer(http.FS(sub))).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("shairport-webui 启动: http://%s （shairport 服务: %s）", cfg.Listen, cfg.ShairportService)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	// 信号处理：优雅退出
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("收到退出信号，正在关闭…")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
}

// defaultPipePath 从 shairport conf 解析 metadata.pipe_name，失败用默认路径。
func defaultPipePath(cfg *webuiconf.Config) string {
	if cfg.MetadataPipe != "" {
		return cfg.MetadataPipe
	}
	f, err := config.Parse(cfg.ShairportConf)
	if err != nil {
		return "/tmp/shairport-sync-metadata"
	}
	if s, ok := f.Get("metadata", "pipe_name"); ok && !s.Commented && s.Value != "" {
		return trimQuotes(s.Value)
	}
	return "/tmp/shairport-sync-metadata"
}

// trimQuotes 去掉 libconfig 字符串值的双引号。
func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
