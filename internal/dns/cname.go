package dns

import (
	"log"
	"net"
	"strings"

	"github.com/miekg/dns"
)

// CNAMEChain 表示 CNAME 链
type CNAMEChain struct {
	// 源域名到目标域名的映射
	links map[string]string
	// 链中的所有域名
	domains map[string]bool
}

// NewCNAMEChain 创建一个新的 CNAME 链
func NewCNAMEChain() *CNAMEChain {
	return &CNAMEChain{
		links:   make(map[string]string),
		domains: make(map[string]bool),
	}
}

// BuildFromResponse 从 DNS 响应中构建 CNAME 链
func (c *CNAMEChain) BuildFromResponse(resp *dns.Msg) {
	if resp == nil || len(resp.Answer) == 0 {
		return
	}

	// 提取所有 CNAME 记录
	for _, ans := range resp.Answer {
		if cname, ok := ans.(*dns.CNAME); ok {
			source := normalizeDomain(cname.Hdr.Name)
			target := normalizeDomain(cname.Target)

			c.links[source] = target
			c.domains[source] = true
			c.domains[target] = true

			log.Printf("CNAME 链: %s -> %s", source, target)
		}
	}
}

// Contains 检查域名是否在 CNAME 链中
func (c *CNAMEChain) Contains(domain string) bool {
	domain = normalizeDomain(domain)
	return c.domains[domain]
}

// GetTarget 获取域名的目标域名
func (c *CNAMEChain) GetTarget(domain string) string {
	domain = normalizeDomain(domain)
	return c.links[domain]
}

// GetAllDomains 获取 CNAME 链中的所有域名
func (c *CNAMEChain) GetAllDomains() []string {
	domains := make([]string, 0, len(c.domains))
	for domain := range c.domains {
		domains = append(domains, domain)
	}
	return domains
}

// TraceChain 跟踪 CNAME 链，返回从源域名到最终目标的所有域名
func (c *CNAMEChain) TraceChain(sourceDomain string) []string {
	sourceDomain = normalizeDomain(sourceDomain)
	if !c.domains[sourceDomain] {
		return nil
	}

	var chain []string
	chain = append(chain, sourceDomain)

	current := sourceDomain
	for {
		target, exists := c.links[current]
		if !exists {
			break
		}
		chain = append(chain, target)
		current = target
	}

	return chain
}

// normalizeDomain 标准化域名（去掉末尾的点，转为小写）
func normalizeDomain(domain string) string {
	if len(domain) > 0 && domain[len(domain)-1] == '.' {
		domain = domain[:len(domain)-1]
	}
	return strings.ToLower(domain)
}

// ExtractCDNIPs 从 DNS 响应中提取 CDN IP
func ExtractCDNIPs(resp *dns.Msg, chain *CNAMEChain, cidrMatcher func(net.IP) bool) []net.IP {
	if resp == nil || len(resp.Answer) == 0 {
		return nil
	}

	var cdnIPs []net.IP

	// 提取所有 A 记录
	for _, ans := range resp.Answer {
		if a, ok := ans.(*dns.A); ok {
			owner := normalizeDomain(a.Hdr.Name)
			ip := a.A

			// 如果 A 记录属于 CNAME 链中的域名，检查 IP 是否属于 CDN
			if chain.Contains(owner) {
				if cidrMatcher(ip) {
					cdnIPs = append(cdnIPs, ip)
					log.Printf("CDN IP: %s 属于域名: %s", ip.String(), owner)
				} else {
					log.Printf("非 CDN IP: %s 属于域名: %s", ip.String(), owner)
				}
			}
		}
	}

	return cdnIPs
}
