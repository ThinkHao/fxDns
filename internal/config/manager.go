package config

import (
	"errors"
	"fmt" // 添加 fmt 包
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ConfigManager 配置管理器，负责配置的加载、验证和热加载
type ConfigManager struct {
	configFilePath  string
	config          *Config
	lastLoadTime    time.Time
	reloadLock      sync.RWMutex
	listeners       []ConfigChangeListener
	mu              sync.RWMutex
	watcher         *fsnotify.Watcher
	initialLoadDone bool
	stopWatcherChan chan struct{} // 用于通知 runWatcherLoop 停止
	watchingStarted bool          // 标记监控是否已启动
}

// ConfigChangeListener 配置变更监听器接口
type ConfigChangeListener interface {
	OnConfigChange(oldConfig, newConfig *Config)
}

// NewConfigManager 创建新的配置管理器
func NewConfigManager(configFilePath string) *ConfigManager {
	return &ConfigManager{
		configFilePath:  configFilePath,
		listeners:       make([]ConfigChangeListener, 0),
		stopWatcherChan: make(chan struct{}), // 初始化时创建，但可能在 StartWatching 中重新创建
	}
}

// LoadConfig 加载配置
func (m *ConfigManager) LoadConfig() error {
	m.reloadLock.Lock()
	defer m.reloadLock.Unlock()

	// 检查配置文件是否存在
	if _, err := os.Stat(m.configFilePath); os.IsNotExist(err) {
		return errors.New("配置文件不存在: " + m.configFilePath)
	}

	// 加载配置
	cfg, err := LoadConfig(m.configFilePath)
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
	m.initialLoadDone = true

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

// runWatcherLoop 在一个单独的 goroutine 中运行，监控配置文件更改
func (m *ConfigManager) runWatcherLoop() {
	defer m.watcher.Close()
	for {
		select {
		case event, ok := <-m.watcher.Events:
			if !ok {
				log.Println("fsnotify watcher.Events 通道已关闭")
				return
			}
			// 调试日志，输出收到的事件和当前的 configFilePath
			log.Printf("[DEBUG] ConfigManager Watcher: Event received for file '%s' (Op: %s). Expected config file: '%s'", event.Name, event.Op.String(), m.configFilePath)

			// 检查事件是否与我们关心的配置文件相关
			// 并且是写入或创建事件
			pathMatch := event.Name == m.configFilePath
			log.Printf("[DEBUG] ConfigManager Watcher: Path comparison result (event.Name == m.configFilePath): %t", pathMatch)

			if pathMatch {
				if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
					log.Printf("ConfigManager 检测到配置文件变化: %s (操作: %s)", event.Name, event.Op.String())
					if err := m.LoadConfig(); err != nil { // LoadConfig 会调用 notifyListeners
						log.Printf("ConfigManager 重新加载配置失败: %v", err)
					} else {
						log.Printf("ConfigManager 成功重新加载配置并已通知监听器")
					}
				}
			} else if filepath.Clean(event.Name) == filepath.Clean(m.configFilePath) &&
					  (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
				log.Printf("配置文件 %s 被移除或重命名 (操作: %s). 如果文件被重新创建，Create 事件应触发重载。", event.Name, event.Op.String())
				// 注意：如果文件被永久删除或移走，监控可能会中断。
				// 更健壮的实现可能需要尝试重新添加对目录的监控，或者处理监控中断的情况。
			}
		case err, ok := <-m.watcher.Errors:
			if !ok {
				log.Println("fsnotify watcher.Errors 通道已关闭")
				return
			}
			log.Printf("ConfigManager 配置文件监控错误: %v", err)
		case <-m.stopWatcherChan:
			log.Println("ConfigManager 监控 goroutine 收到停止信号，退出...")
			return
		}
	}
}

// StartWatching 开始监视配置文件变化
func (m *ConfigManager) StartWatching() error {
	m.mu.Lock()
	if m.watchingStarted {
		m.mu.Unlock()
		log.Println("ConfigManager 监控已经启动，跳过重复启动。")
		return nil
	}
	// 标记尝试启动，如果后续失败，理想情况下应重置此状态，但对于单次启动模型，这可以简化
	m.watchingStarted = true
	configAlreadyLoaded := m.initialLoadDone
	m.mu.Unlock()

	if !configAlreadyLoaded {
		log.Println("ConfigManager 尝试启动监控前，配置尚未加载，执行首次加载...")
		// LoadConfig 内部会设置 initialLoadDone
		if err := m.LoadConfig(); err != nil { // 修复：m.LoadConfig() 只返回一个 error
			m.mu.Lock()
			m.watchingStarted = false // 重置状态，允许重试
			m.mu.Unlock()
			return fmt.Errorf("ConfigManager 启动监控前首次加载配置失败: %w", err)
		}
		log.Println("ConfigManager 首次配置加载完成。")
	} else {
		// 这条日志现在只会在 watchingStarted 为 false 时，且 configAlreadyLoaded 为 true 时打印一次
		log.Println("ConfigManager 配置已由调用者预加载，准备启动监控。")
	}

	log.Printf("ConfigManager 开始监控配置文件目录: %s (针对文件: %s)", filepath.Dir(m.configFilePath), m.configFilePath)

	var err error
	newWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		m.mu.Lock()
		m.watchingStarted = false // 重置状态
		m.mu.Unlock()
		return fmt.Errorf("ConfigManager 创建 fsnotify watcher 失败: %w", err)
	}
	m.watcher = newWatcher

	// 为新的监控循环重新创建/分配 channel
	m.stopWatcherChan = make(chan struct{})
	go m.runWatcherLoop() // 启动事件处理循环

	err = m.watcher.Add(filepath.Dir(m.configFilePath)) // 添加监控目录
	if err != nil {
		m.watcher.Close()        // 清理 watcher
		close(m.stopWatcherChan) // 确保 goroutine 可以退出
		m.mu.Lock()
		m.watchingStarted = false // 重置状态
		m.mu.Unlock()
		return fmt.Errorf("ConfigManager 添加监控路径 '%s' 失败: %w", filepath.Dir(m.configFilePath), err)
	}

	log.Printf("ConfigManager 已成功启动并开始监控配置文件: %s", m.configFilePath) // 修复：使用 configFilePath
	return nil
}

// StopWatching 停止文件监控
func (m *ConfigManager) StopWatching() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.watchingStarted {
		log.Println("ConfigManager 监控尚未启动，无需停止。")
		return
	}

	log.Println("ConfigManager 正在停止文件监控...")
	if m.watcher != nil {
		// 首先关闭 stopWatcherChan 来通知 runWatcherLoop 退出
		// 检查 channel 是否已经关闭，避免重复关闭
		select {
		case <-m.stopWatcherChan:
			// Channel 已经关闭
		default:
			close(m.stopWatcherChan)
		}
		// 然后关闭 fsnotify watcher。Close() 是幂等的。
		// runWatcherLoop 中的 defer m.watcher.Close() 也会尝试关闭，这是安全的。
		m.watcher.Close() 
		m.watcher = nil
	}
	m.watchingStarted = false
	log.Println("ConfigManager 文件监控已停止。")
}

// AddListener 添加配置变更监听器
func (m *ConfigManager) AddListener(listener ConfigChangeListener) {
	m.mu.Lock() // 修复：使用 m.mu 保护 listeners
	defer m.mu.Unlock()
	m.listeners = append(m.listeners, listener)
}

// RemoveListener 移除配置变更监听器
func (m *ConfigManager) RemoveListener(listener ConfigChangeListener) {
	m.mu.Lock() // 修复：使用 m.mu 保护 listeners
	defer m.mu.Unlock()
	for i, l := range m.listeners {
		if l == listener {
			m.listeners = append(m.listeners[:i], m.listeners[i+1:]...)
			break
		}
	}
}

// notifyListeners 通知所有监听器配置已更改
func (m *ConfigManager) notifyListeners(oldConfig, newConfig *Config) {
    m.mu.RLock() // 使用 m.mu 保护 listeners
    listeners := make([]ConfigChangeListener, len(m.listeners))
    copy(listeners, m.listeners)
    m.mu.RUnlock()

    // 同步逐个调用，满足测试对“监听器已被调用”的即时性预期
    for _, l := range listeners {
        func(l ConfigChangeListener) {
            defer func() {
                if r := recover(); r != nil {
                    log.Printf("ConfigManager: 监听器 %T 在 OnConfigChange 中 panic: %v", l, r)
                }
            }()
            l.OnConfigChange(oldConfig, newConfig)
        }(l)
    }
}
