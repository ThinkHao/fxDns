package util

import (
	"testing"
)

func TestDomainMatcher(t *testing.T) {
	matcher := NewDomainMatcher()

	// 测试添加不同类型的模式
	testPatterns := []struct {
		pattern string
		valid   bool
	}{
		{"example.com", true},           // 精确匹配
		{"*.example.com", true},         // 通配符匹配
		{"regex:.*\\.example\\.com", true}, // 正则表达式匹配
		{"", false},                     // 空字符串
		{"*", false},                    // 无效通配符
		{"regex:[", false},              // 无效正则表达式
	}

	for _, tp := range testPatterns {
		// AddPattern 没有返回值，所以我们只能添加有效的模式
		if tp.valid {
			matcher.AddPattern(tp.pattern)
		}
	}

	// 测试域名匹配
	testCases := []struct {
		domain   string
		expected bool
	}{
		{"example.com", true},
		{"sub.example.com", true},       // 应该匹配 *.example.com
		{"test.sub.example.com", true},  // 应该匹配正则表达式
		{"example.org", false},
		{"examplexcom", false},
	}

	for _, tc := range testCases {
		result := matcher.Match(tc.domain)
		if result != tc.expected {
			t.Errorf("域名 '%s' 匹配结果错误, 期望: %v, 实际: %v", tc.domain, tc.expected, result)
		}
	}

	// 测试清除
	matcher.Clear()
	if matcher.Count() != 0 {
		t.Errorf("清除模式失败, 期望数量: 0, 实际数量: %d", matcher.Count())
	}

	// 测试匹配空域名
	if matcher.Match("") {
		t.Error("空域名不应该匹配任何模式")
	}
}
