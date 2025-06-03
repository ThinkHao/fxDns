package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigParsing(t *testing.T) {
	// 创建临时配置文件
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_config.yaml")

	// 写入测试配置
	configContent := `
upstream:
  server: "8.8.8.8:53"
  timeout: "2s"

server:
  listen: "127.0.0.1:53"
  workers: 10
  cache_size: 1000
  cache_ttl: "5m"

cdn_ips:
  - "192.168.1.0/24"
  - "10.0.0.0/8"

domains:
  - pattern: "example.com"
    strategy: "filter"
  - pattern: "*.cdn.com"
    strategy: "replace"
  - pattern: "regex:.*\\.dynamic\\.com"
    strategy: "filter"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("创建测试配置文件失败: %v", err)
	}

	// 加载配置
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// 验证上游服务器配置
	if cfg.Upstream.Server != "8.8.8.8:53" {
		t.Errorf("上游服务器配置错误, 期望: 8.8.8.8:53, 实际: %s", cfg.Upstream.Server)
	}
	if cfg.Upstream.Timeout != 2*time.Second {
		t.Errorf("上游超时配置错误, 期望: 2s, 实际: %s", cfg.Upstream.Timeout)
	}

	// 验证服务器配置
	if cfg.Server.Listen != "127.0.0.1:53" {
		t.Errorf("监听地址配置错误, 期望: 127.0.0.1:53, 实际: %s", cfg.Server.Listen)
	}
	if cfg.Server.Workers != 10 {
		t.Errorf("工作线程配置错误, 期望: 10, 实际: %d", cfg.Server.Workers)
	}
	if cfg.Server.CacheSize != 1000 {
		t.Errorf("缓存大小配置错误, 期望: 1000, 实际: %d", cfg.Server.CacheSize)
	}
	if cfg.Server.CacheTTL != 5*time.Minute {
		t.Errorf("缓存TTL配置错误, 期望: 5m, 实际: %s", cfg.Server.CacheTTL)
	}

	// 验证CDN IP配置
	if len(cfg.CDNIPs) != 2 {
		t.Errorf("CDN IP数量错误, 期望: 2, 实际: %d", len(cfg.CDNIPs))
	}
	if cfg.CDNIPs[0] != "192.168.1.0/24" {
		t.Errorf("CDN IP配置错误, 期望: 192.168.1.0/24, 实际: %s", cfg.CDNIPs[0])
	}

	// 验证域名规则配置
	if len(cfg.Domains) != 3 {
		t.Errorf("域名规则数量错误, 期望: 3, 实际: %d", len(cfg.Domains))
	}
	if cfg.Domains[0].Pattern != "example.com" || cfg.Domains[0].Strategy != "filter" {
		t.Errorf("域名规则配置错误, 期望: example.com/filter, 实际: %s/%s", 
			cfg.Domains[0].Pattern, cfg.Domains[0].Strategy)
	}
	if cfg.Domains[1].Pattern != "*.cdn.com" || cfg.Domains[1].Strategy != "replace" {
		t.Errorf("域名规则配置错误, 期望: *.cdn.com/replace, 实际: %s/%s", 
			cfg.Domains[1].Pattern, cfg.Domains[1].Strategy)
	}
}

func TestInvalidConfig(t *testing.T) {
	// 创建临时配置文件
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid_config.yaml")

	// 写入无效配置
	invalidConfigs := []struct {
		name    string
		content string
	}{
		{
			name: "缺少上游服务器",
			content: `
server:
  listen: "127.0.0.1:53"
  workers: 10
`,
		},
		{
			name: "无效的超时格式",
			content: `
upstream:
  server: "8.8.8.8:53"
  timeout: "invalid"
server:
  listen: "127.0.0.1:53"
`,
		},
		{
			name: "无效的CDN IP",
			content: `
upstream:
  server: "8.8.8.8:53"
  timeout: "2s"
server:
  listen: "127.0.0.1:53"
cdn_ips:
  - "invalid-cidr"
`,
		},
	}

	for _, ic := range invalidConfigs {
		t.Run(ic.name, func(t *testing.T) {
			err := os.WriteFile(configPath, []byte(ic.content), 0644)
			if err != nil {
				t.Fatalf("创建测试配置文件失败: %v", err)
			}

			_, err = LoadConfig(configPath)
			if err == nil {
				t.Errorf("加载无效配置应该返回错误: %s", ic.name)
			}
		})
	}
}
