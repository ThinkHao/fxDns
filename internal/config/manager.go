package config

import (
	"errors"
	"log"
	"os"
	"sync"
	"time"
)

// ConfigManager 配置管理器，负责配置的加载、验证和热加载
type ConfigManager struct {
	configPath    string
	config        *Config
	lastLoadTime  time.Time
	reloadLock    sync.RWMutex
	listeners     []ConfigChangeListener
	listenersLock sync.RWMutex
}

// ConfigChangeListener 配置变更监听器接口
type ConfigChangeListener interface {
	OnConfigChange(oldConfig, newConfig *Config)
}

// NewConfigManager 创建新的配置管理器
func NewConfigManager(configPath string) *ConfigManager {
	return &ConfigManager{
		configPath: configPath,
		listeners:  make([]ConfigChangeListener, 0),
	}
}

// LoadConfig 加载配置
func (m *ConfigManager) LoadConfig() error {
	m.reloadLock.Lock()
	defer m.reloadLock.Unlock()

	// 检查配置文件是否存在
	if _, err := os.Stat(m.configPath); os.IsNotExist(err) {
		return errors.New("配置文件不存在: " + m.configPath)
	}

	// 加载配置
	cfg, err := LoadConfig(m.configPath)
	if err != nil {
		return err
	}

	// 验证配置
	if err := m.validateConfig(cfg); err != nil {
		return err
	}

	// 保存旧配置用于通知监听器
	oldConfig := m.config

	// 更新配置
	m.config = cfg
	m.lastLoadTime = time.Now()

	// 通知配置变更
	if oldConfig != nil {
		m.notifyListeners(oldConfig, cfg)
	}

	return nil
}

// validateConfig 验证配置是否有效
func (m *ConfigManager) validateConfig(cfg *Config) error {
	// 验证上游 DNS 服务器配置
	if cfg.Upstream.Server == "" {
		return errors.New("上游 DNS 服务器地址不能为空")
	}

	// 验证服务器配置
	if cfg.Server.Workers <= 0 {
		return errors.New("工作协程数量必须大于 0")
	}

	// 验证 CDN IP 配置
	if len(cfg.CDNIPs) == 0 {
		return errors.New("CDN IP 列表不能为空")
	}

	// 验证 CIDR 格式
	if err := cfg.parseCIDRs(); err != nil {
		return errors.New("无效的 CIDR 格式: " + err.Error())
	}

	return nil
}

// GetConfig 获取当前配置
func (m *ConfigManager) GetConfig() *Config {
	m.reloadLock.RLock()
	defer m.reloadLock.RUnlock()
	return m.config
}

// StartWatching 开始监视配置文件变化
func (m *ConfigManager) StartWatching() error {
	// 首次加载配置
	if err := m.LoadConfig(); err != nil {
		return err
	}

	// 设置全局配置
	SetGlobalConfig(m.config)

	// 启动配置文件监控
	if err := WatchConfig(m.configPath); err != nil {
		return err
	}

	log.Printf("配置管理器已启动，正在监视配置文件: %s", m.configPath)
	return nil
}

// AddListener 添加配置变更监听器
func (m *ConfigManager) AddListener(listener ConfigChangeListener) {
	m.listenersLock.Lock()
	defer m.listenersLock.Unlock()
	m.listeners = append(m.listeners, listener)
}

// RemoveListener 移除配置变更监听器
func (m *ConfigManager) RemoveListener(listener ConfigChangeListener) {
	m.listenersLock.Lock()
	defer m.listenersLock.Unlock()
	for i, l := range m.listeners {
		if l == listener {
			m.listeners = append(m.listeners[:i], m.listeners[i+1:]...)
			break
		}
	}
}

// notifyListeners 通知所有监听器配置已变更
func (m *ConfigManager) notifyListeners(oldConfig, newConfig *Config) {
	m.listenersLock.RLock()
	listeners := make([]ConfigChangeListener, len(m.listeners))
	copy(listeners, m.listeners)
	m.listenersLock.RUnlock()

	for _, listener := range listeners {
		go listener.OnConfigChange(oldConfig, newConfig)
	}
}
