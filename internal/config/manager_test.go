package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 模拟配置变更监听器
type mockListener struct {
	called    bool
	oldConfig *Config
	newConfig *Config
}

func (m *mockListener) OnConfigChange(old, new *Config) {
	m.called = true
	m.oldConfig = old
	m.newConfig = new
}

func TestConfigManager(t *testing.T) {
	// 创建临时配置文件
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	// 写入初始配置
	initialConfig := `
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

domains:
  - pattern: "example.com"
    strategy: "filter"
`
	err := os.WriteFile(configPath, []byte(initialConfig), 0644)
	if err != nil {
		t.Fatalf("创建测试配置文件失败: %v", err)
	}

	// 创建配置管理器
	manager := NewConfigManager(configPath)
	if err := manager.LoadConfig(); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	// 验证初始配置
	cfg := manager.GetConfig()
	if cfg.Upstream.Server != "8.8.8.8:53" {
		t.Errorf("上游服务器配置错误, 期望: 8.8.8.8:53, 实际: %s", cfg.Upstream.Server)
	}
	if len(cfg.CDNIPs) != 1 || cfg.CDNIPs[0] != "192.168.1.0/24" {
		t.Errorf("CDN IP配置错误, 期望: [192.168.1.0/24], 实际: %v", cfg.CDNIPs)
	}

	// 添加监听器
	listener := &mockListener{}
	manager.AddListener(listener)

	// 修改配置文件
	updatedConfig := `
upstream:
  server: "1.1.1.1:53"
  timeout: "3s"

server:
  listen: "127.0.0.1:53"
  workers: 20
  cache_size: 2000
  cache_ttl: "10m"

cdn_ips:
  - "192.168.1.0/24"
  - "10.0.0.0/8"

domains:
  - pattern: "example.com"
    strategy: "filter"
  - pattern: "*.cdn.com"
    strategy: "replace"
`
	// 写入更新的配置
	err = os.WriteFile(configPath, []byte(updatedConfig), 0644)
	if err != nil {
		t.Fatalf("更新测试配置文件失败: %v", err)
	}

	// 手动触发配置重新加载
	if err := manager.LoadConfig(); err != nil {
		t.Fatalf("重新加载配置失败: %v", err)
	}

	// 验证监听器是否被调用
	if !listener.called {
		t.Error("配置变更监听器未被调用")
	}

	// 验证更新后的配置
	newCfg := manager.GetConfig()
	if newCfg.Upstream.Server != "1.1.1.1:53" {
		t.Errorf("更新后的上游服务器配置错误, 期望: 1.1.1.1:53, 实际: %s", newCfg.Upstream.Server)
	}
	if newCfg.Server.Workers != 20 {
		t.Errorf("更新后的工作线程配置错误, 期望: 20, 实际: %d", newCfg.Server.Workers)
	}
	if len(newCfg.CDNIPs) != 2 {
		t.Errorf("更新后的CDN IP数量错误, 期望: 2, 实际: %d", len(newCfg.CDNIPs))
	}
	if len(newCfg.Domains) != 2 {
		t.Errorf("更新后的域名规则数量错误, 期望: 2, 实际: %d", len(newCfg.Domains))
	}

	// 测试移除监听器
	manager.RemoveListener(listener)
	
	// 再次更新配置
	listener.called = false
	if err := manager.LoadConfig(); err != nil {
		t.Fatalf("第二次重新加载配置失败: %v", err)
	}
	
	// 验证监听器不再被调用
	if listener.called {
		t.Error("移除后的监听器不应该被调用")
	}
}
