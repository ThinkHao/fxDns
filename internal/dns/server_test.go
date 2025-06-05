package dns

import (
	"net"
	"testing"
	"time"

	"github.com/hao/fxdns/internal/config"
	"github.com/hao/fxdns/internal/util"
	"github.com/miekg/dns"
)

// 模拟 DNS 客户端
type mockDNSClient struct {
	responseMsg *dns.Msg
	err         error
}

func (m *mockDNSClient) Exchange(msg *dns.Msg, address string) (*dns.Msg, time.Duration, error) {
	return m.responseMsg, 0, m.err
}

// 模拟 DNS ResponseWriter
type mockResponseWriter struct {
	msg *dns.Msg
}

func (m *mockResponseWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}
}

func (m *mockResponseWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 10053}
}

func (m *mockResponseWriter) WriteMsg(msg *dns.Msg) error {
	m.msg = msg
	return nil
}

func (m *mockResponseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (m *mockResponseWriter) Close() error {
	return nil
}

func (m *mockResponseWriter) TsigStatus() error {
	return nil
}

func (m *mockResponseWriter) TsigTimersOnly(bool) {}

func (m *mockResponseWriter) Hijack() {}

func TestProcessResponse(t *testing.T) {
	// 创建服务器实例
	server := &Server{
		cache:       &Cache{entries: make(map[string]*CacheEntry), maxSize: 100, ttl: 60 * time.Second},
		cidrMatcher: util.NewCIDRMatcher(),
		domainMatcher: util.NewDomainMatcher(),
		config: &config.Config{},
	}

	// 添加测试 CIDR
	server.cidrMatcher.AddCIDRs([]string{"192.168.1.0/24", "10.0.0.0/8"})
	
	// 添加测试域名模式
	server.domainMatcher.AddPattern("example.com")
	server.domainMatcher.AddPattern("*.cdn.com")

	// 创建测试请求
	req := new(dns.Msg)
	req.SetQuestion("test.cdn.com.", dns.TypeA)

	// 测试场景1: 响应包含 CDN IP
	t.Run("包含CDN IP的响应", func(t *testing.T) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		
		// 添加一个 A 记录，包含 CDN IP
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: "test.cdn.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("192.168.1.100"),
		})
		
		// 添加一个 A 记录，不包含 CDN IP
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: "test.cdn.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("172.16.1.1"),
		})

		// 处理响应
		processedResp := server.processResponse(req, resp, []net.IP{net.ParseIP("192.168.1.100")})

		// 验证结果
		if len(processedResp.Answer) != 1 {
			t.Errorf("处理后的响应应该只包含1个答案, 实际: %d", len(processedResp.Answer))
		}

		// 验证保留的是 CDN IP
		if a, ok := processedResp.Answer[0].(*dns.A); ok {
			if !server.cidrMatcher.Contains(a.A) {
				t.Errorf("处理后的响应应该只包含 CDN IP, 实际: %s", a.A)
			}
		} else {
			t.Error("处理后的响应应该包含 A 记录")
		}
	})

	// 测试场景2: 响应不包含 CDN IP
	t.Run("不包含CDN IP的响应", func(t *testing.T) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		
		// 添加两个不包含 CDN IP 的 A 记录
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: "test.cdn.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("172.16.1.1"),
		})
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: "test.cdn.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("172.16.1.2"),
		})

		// 处理响应
		processedResp := server.processResponse(req, resp, nil)

		// 验证结果应该与原始响应相同
		if len(processedResp.Answer) != len(resp.Answer) {
			t.Errorf("处理后的响应答案数量错误, 期望: %d, 实际: %d", 
				len(resp.Answer), len(processedResp.Answer))
		}
	})

	// 测试场景3: CNAME 响应
	t.Run("CNAME响应", func(t *testing.T) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		
		// 添加一个 CNAME 记录
		resp.Answer = append(resp.Answer, &dns.CNAME{
			Hdr:    dns.RR_Header{Name: "test.cdn.com.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 300},
			Target: "cdn.example.org.",
		})
		
		// 添加一个 A 记录，包含 CDN IP
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: "cdn.example.org.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("192.168.1.100"),
		})
		
		// 添加一个 A 记录，不包含 CDN IP
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: "cdn.example.org.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("172.16.1.1"),
		})

		// 处理响应
		processedResp := server.processResponse(req, resp, []net.IP{net.ParseIP("192.168.1.100")})

		// 验证结果
		if len(processedResp.Answer) != 2 {
			t.Errorf("处理后的响应应该包含2个答案 (CNAME + A), 实际: %d", len(processedResp.Answer))
		}

		// 验证第一个记录是 CNAME
		if _, ok := processedResp.Answer[0].(*dns.CNAME); !ok {
			t.Error("处理后的响应第一个记录应该是 CNAME")
		}

		// 验证第二个记录是 A，并且是 CDN IP
		if a, ok := processedResp.Answer[1].(*dns.A); ok {
			if !server.cidrMatcher.Contains(a.A) {
				t.Errorf("处理后的响应应该只包含 CDN IP, 实际: %s", a.A)
			}
		} else {
			t.Error("处理后的响应第二个记录应该是 A 记录")
		}
	})
}

func TestCacheOperations(t *testing.T) {
	// 创建服务器实例
	server := &Server{
		cache: &Cache{
			entries: make(map[string]*CacheEntry),
			maxSize: 2, // 小缓存大小，便于测试
			ttl:     1 * time.Second,
		},
	}

	// 创建测试请求和响应
	req1 := new(dns.Msg)
	req1.SetQuestion("example.com.", dns.TypeA)
	
	resp1 := new(dns.Msg)
	resp1.SetReply(req1)
	resp1.Answer = append(resp1.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   net.ParseIP("192.168.1.1"),
	})

	req2 := new(dns.Msg)
	req2.SetQuestion("example.org.", dns.TypeA)
	
	resp2 := new(dns.Msg)
	resp2.SetReply(req2)
	resp2.Answer = append(resp2.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: "example.org.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   net.ParseIP("192.168.1.2"),
	})

	// 测试缓存更新
	server.updateCache(req1, resp1)
	
	// 验证缓存命中
	cachedResp := server.checkCache(req1)
	if cachedResp == nil {
		t.Error("缓存应该命中")
	}
	
	// 验证缓存未命中
	cachedResp = server.checkCache(req2)
	if cachedResp != nil {
		t.Error("缓存不应该命中")
	}
	
	// 添加第二个缓存项
	server.updateCache(req2, resp2)
	
	// 验证两个缓存项都存在
	if len(server.cache.entries) != 2 {
		t.Errorf("缓存项数量错误, 期望: 2, 实际: %d", len(server.cache.entries))
	}
	
	// 添加第三个缓存项，应该导致一个旧项被删除
	req3 := new(dns.Msg)
	req3.SetQuestion("example.net.", dns.TypeA)
	
	resp3 := new(dns.Msg)
	resp3.SetReply(req3)
	resp3.Answer = append(resp3.Answer, &dns.A{
		Hdr: dns.RR_Header{Name: "example.net.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   net.ParseIP("192.168.1.3"),
	})
	
	server.updateCache(req3, resp3)
	
	// 验证缓存项数量不超过最大值
	if len(server.cache.entries) > server.cache.maxSize {
		t.Errorf("缓存项数量超过最大值, 最大值: %d, 实际: %d", 
			server.cache.maxSize, len(server.cache.entries))
	}
	
	// 测试缓存过期
	time.Sleep(1100 * time.Millisecond) // 等待缓存过期
	
	cachedResp = server.checkCache(req3)
	if cachedResp != nil {
		t.Error("过期的缓存项不应该命中")
	}
}
