package dns

import (
	"net"
	"testing"

	"github.com/hao/fxdns/internal/config"
	"github.com/hao/fxdns/internal/util"
	"github.com/miekg/dns"
)

func TestCNAMEChainDetection(t *testing.T) {
	// 创建一个测试服务器
	server := &Server{
		cidrMatcher:   util.NewCIDRMatcher(),
		domainMatcher: util.NewDomainMatcher(),
		config: &config.Config{
			Domains: []config.DomainRule{
				{Pattern: "example.com", Strategy: config.StrategyFilterNonCDN, TTL: 300},
				{Pattern: "*.example.com", Strategy: config.StrategyFilterNonCDN, TTL: 300},
				{Pattern: "cdn.example.org", Strategy: config.StrategyReturnCDNA, TTL: 60},
			},
		},
	}

	// 添加测试 CIDR
	server.cidrMatcher.AddCIDR("192.168.1.0/24")
	server.cidrMatcher.AddCIDR("10.0.0.0/8")

	// 添加测试域名
	server.domainMatcher.AddPattern("example.com")
	server.domainMatcher.AddPattern("*.example.com")
	server.domainMatcher.AddPattern("cdn.example.org")

	// 创建一个包含 CNAME 链的 DNS 响应
	resp := new(dns.Msg)
	
	// 添加 CNAME 记录: example.com -> cdn.example.com -> cdn.example.org
	cname1 := new(dns.CNAME)
	cname1.Hdr = dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300}
	cname1.Target = "cdn.example.com."
	
	cname2 := new(dns.CNAME)
	cname2.Hdr = dns.RR_Header{Name: "cdn.example.com.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300}
	cname2.Target = "cdn.example.org."
	
	// 添加 A 记录: cdn.example.org -> 192.168.1.1 (CDN IP) 和 8.8.8.8 (非 CDN IP)
	a1 := new(dns.A)
	a1.Hdr = dns.RR_Header{Name: "cdn.example.org.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300}
	a1.A = net.ParseIP("192.168.1.1")
	
	a2 := new(dns.A)
	a2.Hdr = dns.RR_Header{Name: "cdn.example.org.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300}
	a2.A = net.ParseIP("8.8.8.8")
	
	resp.Answer = []dns.RR{cname1, cname2, a1, a2}
	
	// 测试 CNAME 链检测
	containsCDN, cdnIPs := server.checkCNAMEForCDNIP(resp)
	
	if !containsCDN {
		t.Error("应该检测到 CDN IP，但是没有检测到")
	}
	
	if len(cdnIPs) != 1 {
		t.Errorf("应该检测到 1 个 CDN IP，但是检测到了 %d 个", len(cdnIPs))
	}
	
	if cdnIPs[0].String() != "192.168.1.1" {
		t.Errorf("检测到的 CDN IP 不正确，期望 192.168.1.1，实际 %s", cdnIPs[0].String())
	}
	
	// 测试过滤非 CDN IP
	filteredResp := server.filterNonCDNIPs(resp, cdnIPs)
	
	// 检查过滤后的响应是否只包含 CNAME 记录和 CDN IP 的 A 记录
	if len(filteredResp.Answer) != 3 { // 2 个 CNAME + 1 个 CDN IP 的 A 记录
		t.Errorf("过滤后的响应应该包含 3 条记录，但是包含了 %d 条", len(filteredResp.Answer))
	}
	
	// 检查是否包含所有 CNAME 记录
	cnameCount := 0
	for _, rr := range filteredResp.Answer {
		if _, ok := rr.(*dns.CNAME); ok {
			cnameCount++
		}
	}
	if cnameCount != 2 {
		t.Errorf("过滤后的响应应该包含 2 条 CNAME 记录，但是包含了 %d 条", cnameCount)
	}
	
	// 检查是否只包含 CDN IP 的 A 记录
	aCount := 0
	for _, rr := range filteredResp.Answer {
		if a, ok := rr.(*dns.A); ok {
			aCount++
			if !server.cidrMatcher.Contains(a.A) {
				t.Errorf("过滤后的响应包含非 CDN IP: %s", a.A.String())
			}
		}
	}
	if aCount != 1 {
		t.Errorf("过滤后的响应应该包含 1 条 CDN IP 的 A 记录，但是包含了 %d 条", aCount)
	}
}
