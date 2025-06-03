package config

import (
	"io/ioutil"
	"log"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// Config 表示应用程序的配置
type Config struct {
	Upstream UpstreamConfig `yaml:"upstream"`
	Server   ServerConfig   `yaml:"server"`
	CDNIPs   []string       `yaml:"cdn_ips"`
	Domains  []DomainRule   `yaml:"domains"`

	// 用于存储解析后的 CIDR
	parsedCIDRs []*net.IPNet
	mu          sync.RWMutex
}

// UpstreamConfig 表示上游 DNS 服务器的配置
type UpstreamConfig struct {
	Server  string        `yaml:"server"`
	Timeout time.Duration `yaml:"timeout"`
}

// ServerConfig 表示 DNS 服务器的配置
type ServerConfig struct {
	Listen    string        `yaml:"listen"`
	Workers   int           `yaml:"workers"`
	CacheSize int           `yaml:"cache_size"`
	CacheTTL  time.Duration `yaml:"cache_ttl"`
}

// DomainRule 表示域名处理规则
type DomainRule struct {
	Pattern  string  `yaml:"pattern"`
	Strategy string  `yaml:"strategy"`
	TTL      uint32  `yaml:"ttl"`       // 返回给客户端的 TTL 值（秒）
}

// 策略常量
const (
	StrategyFilterNonCDN = "filter_non_cdn"
	StrategyReturnCDNA   = "return_cdn_a"
	StrategyNone         = "none"
)

// 全局配置实例
var (
	globalConfig *Config
	configMutex  sync.RWMutex
)

// LoadConfig 从文件加载配置
func LoadConfig(configPath string) (*Config, error) {
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// 解析 CIDR
	if err := cfg.parseCIDRs(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// parseCIDRs 解析 CIDR 格式的 IP 地址段
func (c *Config) parseCIDRs() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.parsedCIDRs = make([]*net.IPNet, 0, len(c.CDNIPs))
	for _, cidrStr := range c.CDNIPs {
		_, cidr, err := net.ParseCIDR(cidrStr)
		if err != nil {
			return err
		}
		c.parsedCIDRs = append(c.parsedCIDRs, cidr)
	}
	return nil
}

// IsCDNIP 检查 IP 是否属于 CDN 节点
func (c *Config) IsCDNIP(ip net.IP) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, cidr := range c.parsedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// GetDomainStrategy 获取域名的处理策略
func (c *Config) GetDomainStrategy(domain string) string {
	for _, rule := range c.Domains {
		if MatchDomain(rule.Pattern, domain) {
			return rule.Strategy
		}
	}
	return StrategyNone
}

// MatchDomain 检查域名是否匹配模式（支持泛域名）
func MatchDomain(pattern, domain string) bool {
	// 如果域名以点结尾，去掉最后的点
	if len(domain) > 0 && domain[len(domain)-1] == '.' {
		domain = domain[:len(domain)-1]
	}
	
	// 精确匹配
	if pattern == domain {
		return true
	}
	
	// 泛域名匹配
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // 包含开头的点
		
		// 检查是否以后缀结尾
		if strings.HasSuffix(domain, suffix) {
			return true
		}
		
		// 检查子域名
		parts := strings.Split(domain, ".")
		if len(parts) >= 2 {
			// 构建可能的匹配域名
			for i := 1; i < len(parts); i++ {
				subDomain := "*." + strings.Join(parts[i:], ".")
				if subDomain == pattern {
					return true
				}
			}
		}
	}
	
	// 正则表达式匹配
	if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
		// 将通配符转换为正则表达式
		regexPattern := strings.Replace(pattern, ".", "\\.", -1)
		regexPattern = strings.Replace(regexPattern, "*", ".*", -1)
		regexPattern = strings.Replace(regexPattern, "?", ".", -1)
		regexPattern = "^" + regexPattern + "$"
		
		reg, err := regexp.Compile(regexPattern)
		if err == nil && reg.MatchString(domain) {
			return true
		}
	}
	
	return false
}

// GetGlobalConfig 获取全局配置
func GetGlobalConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return globalConfig
}

// SetGlobalConfig 设置全局配置
func SetGlobalConfig(cfg *Config) {
	configMutex.Lock()
	defer configMutex.Unlock()
	globalConfig = cfg
}

// WatchConfig 监视配置文件变化并自动重新加载
func WatchConfig(configPath string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Printf("检测到配置文件变化: %s", event.Name)
					// 配置文件已修改，重新加载
					cfg, err := LoadConfig(configPath)
					if err == nil {
						log.Printf("重新加载配置成功")
						SetGlobalConfig(cfg)
					} else {
						log.Printf("重新加载配置失败: %v", err)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("配置文件监控错误: %v", err)
			}
		}
	}()

	// 监视配置文件目录
	dirPath := filepath.Dir(configPath)
	log.Printf("开始监控配置文件目录: %s", dirPath)
	return watcher.Add(dirPath)
}
