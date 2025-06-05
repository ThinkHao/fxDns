package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hao/fxdns/internal/dns"
)

var (
	configPath string
)

func init() {
	// 解析命令行参数
	flag.StringVar(&configPath, "config", "config/config.yaml", "配置文件路径")
	flag.Parse()

	// 确保配置文件路径是绝对路径
	if !filepath.IsAbs(configPath) {
		absPath, err := filepath.Abs(configPath)
		if err == nil {
			configPath = absPath
		}
	}
}

func main() {
	// 创建并启动 DNS 服务器
	server, err := dns.NewServer(configPath)
	if err != nil {
		log.Fatalf("创建 DNS 服务器失败: %v", err)
	}

	// 启动 DNS 服务器及其配置监控
	// server.Start() 现在会处理服务器的启动以及配置监控的启动。
	// 它内部会将 ListenAndServe 放到一个 goroutine 中，所以 Start() 本身不会永久阻塞。
	if err := server.Start(); err != nil {
		log.Fatalf("无法启动服务器或配置监控: %s", err)
	}

	// 等待信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	// 优雅关闭
	log.Println("正在关闭 DNS 服务器...")
	if err := server.Stop(); err != nil {
		log.Printf("关闭 DNS 服务器时出错: %v", err)
	}
	log.Println("DNS 服务器已关闭")
}
