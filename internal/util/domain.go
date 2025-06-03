package util

import (
	"regexp"
	"strings"
	"sync"
)

// DomainMatcher 域名匹配器，用于高效匹配域名是否符合特定模式
type DomainMatcher struct {
	patterns     []string
	regexCache   map[string]*regexp.Regexp
	exactMatches map[string]bool
	mu           sync.RWMutex
}

// NewDomainMatcher 创建新的域名匹配器
func NewDomainMatcher() *DomainMatcher {
	return &DomainMatcher{
		patterns:     make([]string, 0),
		regexCache:   make(map[string]*regexp.Regexp),
		exactMatches: make(map[string]bool),
	}
}

// AddPattern 添加域名匹配模式
func (m *DomainMatcher) AddPattern(pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已存在
	for _, p := range m.patterns {
		if p == pattern {
			return
		}
	}

	m.patterns = append(m.patterns, pattern)

	// 如果是精确匹配模式，添加到精确匹配映射
	if !strings.Contains(pattern, "*") && !strings.Contains(pattern, "?") {
		m.exactMatches[pattern] = true
	} else if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
		// 预编译正则表达式
		m.compileRegex(pattern)
	}
}

// compileRegex 将通配符模式编译为正则表达式
func (m *DomainMatcher) compileRegex(pattern string) {
	// 转义特殊字符
	regexPattern := strings.Replace(pattern, ".", "\\.", -1)
	// 将通配符转换为正则表达式
	regexPattern = strings.Replace(regexPattern, "*", ".*", -1)
	regexPattern = strings.Replace(regexPattern, "?", ".", -1)
	regexPattern = "^" + regexPattern + "$"

	if reg, err := regexp.Compile(regexPattern); err == nil {
		m.regexCache[pattern] = reg
	}
}

// RemovePattern 移除域名匹配模式
func (m *DomainMatcher) RemovePattern(pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, p := range m.patterns {
		if p == pattern {
			m.patterns = append(m.patterns[:i], m.patterns[i+1:]...)
			delete(m.exactMatches, pattern)
			delete(m.regexCache, pattern)
			break
		}
	}
}

// Match 检查域名是否匹配任何模式
func (m *DomainMatcher) Match(domain string) bool {
	// 标准化域名
	domain = normalizeDomain(domain)

	m.mu.RLock()
	defer m.mu.RUnlock()

	// 首先检查精确匹配
	if m.exactMatches[domain] {
		return true
	}

	// 然后检查泛域名匹配
	for _, pattern := range m.patterns {
		if m.matchPattern(pattern, domain) {
			return true
		}
	}

	return false
}

// matchPattern 检查域名是否匹配特定模式
func (m *DomainMatcher) matchPattern(pattern, domain string) bool {
	// 精确匹配
	if pattern == domain {
		return true
	}

	// 泛域名匹配 (*.example.com)
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // 包含开头的点
		
		// 检查是否以后缀结尾
		if domain == suffix[1:] { // 去掉点后的部分完全匹配
			return false // 不匹配根域名
		}
		
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
		if reg, ok := m.regexCache[pattern]; ok {
			return reg.MatchString(domain)
		}
	}

	return false
}

// GetPatterns 获取所有匹配模式
func (m *DomainMatcher) GetPatterns() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, len(m.patterns))
	copy(result, m.patterns)
	return result
}

// Clear 清除所有匹配模式
func (m *DomainMatcher) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.patterns = make([]string, 0)
	m.regexCache = make(map[string]*regexp.Regexp)
	m.exactMatches = make(map[string]bool)
}

// Count 返回匹配模式数量
func (m *DomainMatcher) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.patterns)
}

// normalizeDomain 标准化域名
func normalizeDomain(domain string) string {
	// 去掉末尾的点
	if len(domain) > 0 && domain[len(domain)-1] == '.' {
		domain = domain[:len(domain)-1]
	}
	return strings.ToLower(domain)
}

// MatchDomain 检查域名是否匹配模式（静态方法）
func MatchDomain(pattern, domain string) bool {
	// 标准化域名和模式
	domain = normalizeDomain(domain)
	pattern = strings.ToLower(pattern)

	// 精确匹配
	if pattern == domain {
		return true
	}

	// 泛域名匹配
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // 包含开头的点
		
		// 检查是否以后缀结尾
		if domain == suffix[1:] { // 去掉点后的部分完全匹配
			return false // 不匹配根域名
		}
		
		if strings.HasSuffix(domain, suffix) {
			return true
		}
	}

	// 正则表达式匹配
	if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
		// 转义特殊字符
		regexPattern := strings.Replace(pattern, ".", "\\.", -1)
		// 将通配符转换为正则表达式
		regexPattern = strings.Replace(regexPattern, "*", ".*", -1)
		regexPattern = strings.Replace(regexPattern, "?", ".", -1)
		regexPattern = "^" + regexPattern + "$"
		
		if reg, err := regexp.Compile(regexPattern); err == nil {
			return reg.MatchString(domain)
		}
	}

	return false
}
