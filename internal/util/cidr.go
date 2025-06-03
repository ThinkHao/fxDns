package util

import (
	"net"
	"sort"
	"sync"
)

// CIDRMatcher CIDR 匹配器，用于高效匹配 IP 地址是否在 CIDR 范围内
type CIDRMatcher struct {
	cidrs []*net.IPNet
	mu    sync.RWMutex
}

// NewCIDRMatcher 创建新的 CIDR 匹配器
func NewCIDRMatcher() *CIDRMatcher {
	return &CIDRMatcher{
		cidrs: make([]*net.IPNet, 0),
	}
}

// AddCIDR 添加 CIDR 到匹配器
func (m *CIDRMatcher) AddCIDR(cidrStr string) error {
	_, cidr, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已存在
	for _, existing := range m.cidrs {
		if existing.String() == cidr.String() {
			return nil
		}
	}

	m.cidrs = append(m.cidrs, cidr)
	return nil
}

// AddCIDRs 批量添加 CIDR 到匹配器
func (m *CIDRMatcher) AddCIDRs(cidrStrs []string) error {
	for _, cidrStr := range cidrStrs {
		if err := m.AddCIDR(cidrStr); err != nil {
			return err
		}
	}
	return nil
}

// RemoveCIDR 从匹配器中移除 CIDR
func (m *CIDRMatcher) RemoveCIDR(cidrStr string) {
	_, cidr, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i, existing := range m.cidrs {
		if existing.String() == cidr.String() {
			m.cidrs = append(m.cidrs[:i], m.cidrs[i+1:]...)
			break
		}
	}
}

// Contains 检查 IP 是否在任何 CIDR 范围内
func (m *CIDRMatcher) Contains(ip net.IP) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, cidr := range m.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// GetCIDRs 获取所有 CIDR
func (m *CIDRMatcher) GetCIDRs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, len(m.cidrs))
	for i, cidr := range m.cidrs {
		result[i] = cidr.String()
	}

	// 排序以保持一致性
	sort.Strings(result)
	return result
}

// Clear 清除所有 CIDR
func (m *CIDRMatcher) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cidrs = make([]*net.IPNet, 0)
}

// Count 返回 CIDR 数量
func (m *CIDRMatcher) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.cidrs)
}

// IPInCIDRs 检查 IP 是否在给定的 CIDR 列表中
// 这是一个便捷的静态方法，不需要创建 CIDRMatcher 实例
func IPInCIDRs(ip net.IP, cidrStrs []string) bool {
	for _, cidrStr := range cidrStrs {
		_, cidr, err := net.ParseCIDR(cidrStr)
		if err == nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}
