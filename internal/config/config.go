package config

import (
	"fmt"
	"io/ioutil"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

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

// Validate 对配置进行基本校验
func (c *Config) Validate() error {
    // 验证上游 DNS 服务器配置
    if strings.TrimSpace(c.Upstream.Server) == "" {
        return fmt.Errorf("上游 DNS 服务器地址不能为空")
    }
    // 验证服务器工作协程数量
    if c.Server.Workers <= 0 {
        return fmt.Errorf("工作协程数量必须大于 0")
    }
    // 验证 CDN IP 列表
    if len(c.CDNIPs) == 0 {
        return fmt.Errorf("CDN IP 列表不能为空")
    }
    return nil
}

// UpstreamConfig 表示上游 DNS 服务器的配置
type UpstreamConfig struct {
	Server          string        `yaml:"server"`
	FallbackServer  string        `yaml:"fallback_server"`
	Timeout         time.Duration `yaml:"timeout"`
	NoRecordNoFallback bool        `yaml:"no_record_no_fallback"`
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
	Pattern               string  `yaml:"pattern"`
	Strategy              string  `yaml:"strategy"`
	TTL                   uint32  `yaml:"ttl"`       // 返回给客户端的 TTL 值（秒）
	StripCNAMEWhenNoRecord bool    `yaml:"strip_cname_when_no_record"`
	NoRecordNoFallback    *bool   `yaml:"no_record_no_fallback"`
}

// 策略常量
const (
	StrategyFilterNonCDN = "filter_non_cdn"
	StrategyReturnCDNA   = "return_cdn_a"
	StrategyNone         = "none"
)

// 全局配置实例

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

	// 基本校验，确保与单测期望一致
	if err := cfg.Validate(); err != nil {
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
