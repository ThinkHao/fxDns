package util

import (
	"net"
	"testing"
)

func TestCIDRMatcher(t *testing.T) {
	matcher := NewCIDRMatcher()

	// 测试添加CIDR
	err := matcher.AddCIDRs([]string{"192.168.1.0/24", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("添加CIDR失败: %v", err)
	}

	// 测试包含的IP
	testCases := []struct {
		ip       string
		expected bool
	}{
		{"192.168.1.100", true},
		{"192.168.2.1", false},
		{"10.1.1.1", true},
		{"172.16.0.1", false},
	}

	for _, tc := range testCases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("解析IP失败: %s", tc.ip)
		}

		result := matcher.Contains(ip)
		if result != tc.expected {
			t.Errorf("IP %s 匹配结果错误, 期望: %v, 实际: %v", tc.ip, tc.expected, result)
		}
	}

	// 测试清除
	matcher.Clear()
	if matcher.Count() != 0 {
		t.Errorf("清除CIDR失败, 期望数量: 0, 实际数量: %d", matcher.Count())
	}

	// 测试添加无效CIDR
	err = matcher.AddCIDRs([]string{"invalid-cidr"})
	if err == nil {
		t.Error("添加无效CIDR应该返回错误")
	}
}
