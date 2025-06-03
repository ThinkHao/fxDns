package dns

import (
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hao/fxdns/internal/config"
	"github.com/hao/fxdns/internal/util"
	"github.com/miekg/dns"
)

// Server 表示 DNS 代理服务器
type Server struct {
	server        *dns.Server
	client        *dns.Client
	upstream      string
	timeout       time.Duration
	config        *config.Config
	cache         *Cache
	workerPool    chan struct{}
	cidrMatcher   *util.CIDRMatcher
	domainMatcher *util.DomainMatcher
	configManager *config.ConfigManager
}

// Cache 表示 DNS 缓存
type Cache struct {
	entries map[string]*CacheEntry
	mu      sync.RWMutex
	maxSize int
	ttl     time.Duration
}

// CacheEntry 表示缓存条目
type CacheEntry struct {
	msg      *dns.Msg
	expireAt time.Time
}

// NewServer 创建一个新的 DNS 代理服务器
func NewServer(configPath string) (*Server, error) {
	// 创建配置管理器
	configManager := config.NewConfigManager(configPath)
	if err := configManager.LoadConfig(); err != nil {
		return nil, err
	}
	
	cfg := configManager.GetConfig()
	
	// 创建缓存
	cache := &Cache{
		entries: make(map[string]*CacheEntry),
		maxSize: cfg.Server.CacheSize,
		ttl:     cfg.Server.CacheTTL,
	}

	// 创建工作池
	workerPool := make(chan struct{}, cfg.Server.Workers)
	for i := 0; i < cfg.Server.Workers; i++ {
		workerPool <- struct{}{}
	}

	// 创建 CIDR 匹配器
	cidrMatcher := util.NewCIDRMatcher()
	if err := cidrMatcher.AddCIDRs(cfg.CDNIPs); err != nil {
		return nil, err
	}

	// 创建域名匹配器
	domainMatcher := util.NewDomainMatcher()
	for _, rule := range cfg.Domains {
		domainMatcher.AddPattern(rule.Pattern)
	}

	server := &Server{
		client: &dns.Client{
			Net:     "udp",
			Timeout: cfg.Upstream.Timeout,
		},
		upstream:      cfg.Upstream.Server,
		timeout:       cfg.Upstream.Timeout,
		config:        cfg,
		cache:         cache,
		workerPool:    workerPool,
		cidrMatcher:   cidrMatcher,
		domainMatcher: domainMatcher,
		configManager: configManager,
	}

	// 注册配置变更监听器
	configManager.AddListener(server)

	return server, nil
}

// Start 启动 DNS 代理服务器
func (s *Server) Start() error {
	// 启动配置监控
	if err := s.configManager.StartWatching(); err != nil {
		return err
	}

	dns.HandleFunc(".", s.handleDNSRequest)

	s.server = &dns.Server{
		Addr:    s.config.Server.Listen,
		Net:     "udp",
		Handler: dns.DefaultServeMux,
	}

	log.Printf("DNS 代理服务器启动在 %s", s.config.Server.Listen)
	return s.server.ListenAndServe()
}

// Stop 停止 DNS 代理服务器
func (s *Server) Stop() error {
	if s.server != nil {
		return s.server.Shutdown()
	}
	return nil
}

// handleDNSRequest 处理 DNS 请求
func (s *Server) handleDNSRequest(w dns.ResponseWriter, r *dns.Msg) {
	// 获取工作池令牌
	<-s.workerPool
	defer func() {
		s.workerPool <- struct{}{}
	}()

	// 检查缓存
	if resp := s.checkCache(r); resp != nil {
		w.WriteMsg(resp)
		return
	}

	// 转发请求到上游 DNS 服务器
	resp, err := s.forwardRequest(r)
	if err != nil {
		log.Printf("转发请求失败: %v", err)
		dns.HandleFailed(w, r)
		return
	}

	// 处理响应
	processedResp := s.processResponse(r, resp)

	// 更新缓存
	s.updateCache(r, processedResp)

	// 发送响应
	w.WriteMsg(processedResp)
}

// forwardRequest 将请求转发到上游 DNS 服务器
func (s *Server) forwardRequest(r *dns.Msg) (*dns.Msg, error) {
	resp, _, err := s.client.Exchange(r, s.upstream)
	return resp, err
}

// processResponse 处理 DNS 响应
func (s *Server) processResponse(req, resp *dns.Msg) *dns.Msg {
	if len(req.Question) == 0 || resp == nil {
		return resp
	}

	// 获取请求的域名
	domain := normalizeDomain(req.Question[0].Name)

	// 检查域名是否匹配任何规则
	if !s.domainMatcher.Match(domain) {
		// 构建 CNAME 链，检查链中是否有匹配的域名
		chain := NewCNAMEChain()
		chain.BuildFromResponse(resp)
		
		// 检查 CNAME 链中是否有匹配的域名
		matchFound := false
		for domainInChain := range chain.domains {
			if s.domainMatcher.Match(domainInChain) {
				matchFound = true
				log.Printf("在 CNAME 链中找到匹配的域名: %s", domainInChain)
				break
			}
		}
		
		if !matchFound {
			return resp // 没有匹配的域名，直接返回原始响应
		}
	}

	// 获取域名的处理策略
	strategy := s.config.GetDomainStrategy(domain)
	if strategy == config.StrategyNone {
		// 检查 CNAME 链中是否有匹配的域名及其策略
		chain := NewCNAMEChain()
		chain.BuildFromResponse(resp)
		
		for domainInChain := range chain.domains {
			strategyInChain := s.config.GetDomainStrategy(domainInChain)
			if strategyInChain != config.StrategyNone {
				strategy = strategyInChain
				log.Printf("使用 CNAME 链中域名 %s 的策略: %s", domainInChain, strategy)
				break
			}
		}
		
		if strategy == config.StrategyNone {
			return resp // 没有找到处理策略，直接返回原始响应
		}
	}

	// 构建 CNAME 链并检查 CDN IP
	chain := NewCNAMEChain()
	chain.BuildFromResponse(resp)
	cdnIPs := ExtractCDNIPs(resp, chain, s.cidrMatcher.Contains)
	
	if len(cdnIPs) == 0 {
		log.Printf("未检测到 CDN IP，返回原始响应")
		return resp
	}

	// 根据策略处理响应
	switch strategy {
	case config.StrategyFilterNonCDN:
		log.Printf("使用策略: 过滤非 CDN IP，发现 %d 个 CDN IP", len(cdnIPs))
		return s.filterNonCDNIPs(resp, cdnIPs)
	case config.StrategyReturnCDNA:
		log.Printf("使用策略: 直接返回 CDN A 记录，发现 %d 个 CDN IP", len(cdnIPs))
		return s.returnCDNARecords(req, cdnIPs)
	default:
		return resp
	}
}

// checkCNAMEForCDNIP 检查 CNAME 记录是否解析到 CDN 节点 IP
func (s *Server) checkCNAMEForCDNIP(resp *dns.Msg) (bool, []net.IP) {
	var cdnIPs []net.IP
	var cnameTargets = make(map[string]bool)
	
	// 首先提取所有 CNAME 记录，建立 CNAME 链
	for _, ans := range resp.Answer {
		if cname, ok := ans.(*dns.CNAME); ok {
			// 将 CNAME 目标添加到映射中
			target := cname.Target
			// 标准化域名
			if len(target) > 0 && target[len(target)-1] == '.' {
				target = target[:len(target)-1]
			}
			target = strings.ToLower(target)
			cnameTargets[target] = true
			
			// 检查 CNAME 目标是否在我们的域名匹配器中
			if s.domainMatcher.Match(target) {
				log.Printf("检测到 CNAME 链中的目标域名匹配规则: %s", target)
			}
		}
	}

	// 遍历所有 A 记录
	for _, ans := range resp.Answer {
		if a, ok := ans.(*dns.A); ok {
			ip := a.A
			
			// 检查该 A 记录是否属于 CNAME 链中的域名
			hdr := a.Header()
			owner := hdr.Name
			if len(owner) > 0 && owner[len(owner)-1] == '.' {
				owner = owner[:len(owner)-1]
			}
			owner = strings.ToLower(owner)
			
			// 如果该 A 记录属于 CNAME 链或者原始域名匹配我们的规则
			if cnameTargets[owner] || s.domainMatcher.Match(owner) {
				// 检查 IP 是否属于 CDN IP
				if s.cidrMatcher.Contains(ip) {
					cdnIPs = append(cdnIPs, ip)
					log.Printf("检测到 CDN IP: %s 属于域名: %s", ip.String(), owner)
				}
			}
		}
	}

	return len(cdnIPs) > 0, cdnIPs
}

// filterNonCDNIPs 过滤掉非 CDN 节点的 IP
func (s *Server) filterNonCDNIPs(resp *dns.Msg, cdnIPs []net.IP) *dns.Msg {
	// 创建新的响应
	newResp := resp.Copy()
	newResp.Answer = make([]dns.RR, 0, len(resp.Answer))

	// 构建 CNAME 链映射
	cnameMap := make(map[string]string) // 源域名 -> 目标域名
	for _, ans := range resp.Answer {
		if cname, ok := ans.(*dns.CNAME); ok {
			source := cname.Hdr.Name
			if len(source) > 0 && source[len(source)-1] == '.' {
				source = source[:len(source)-1]
			}
			source = strings.ToLower(source)

			target := cname.Target
			if len(target) > 0 && target[len(target)-1] == '.' {
				target = target[:len(target)-1]
			}
			target = strings.ToLower(target)

			cnameMap[source] = target
			
			// 保留所有 CNAME 记录
			newResp.Answer = append(newResp.Answer, cname)
		}
	}

	// 收集所有匹配的域名
	matchedDomains := make(map[string]bool)
	for domain := range cnameMap {
		if s.domainMatcher.Match(domain) {
			matchedDomains[domain] = true
			
			// 跟踪 CNAME 链
			current := domain
			for {
				target, exists := cnameMap[current]
				if !exists {
					break
				}
				matchedDomains[target] = true
				current = target
			}
		}
	}

	// 只添加属于匹配域名的 CDN IP 的 A 记录
	for _, ans := range resp.Answer {
		if a, ok := ans.(*dns.A); ok {
			owner := a.Hdr.Name
			if len(owner) > 0 && owner[len(owner)-1] == '.' {
				owner = owner[:len(owner)-1]
			}
			owner = strings.ToLower(owner)

			// 如果 A 记录属于匹配的域名或者 CNAME 链中的域名
			if matchedDomains[owner] || s.domainMatcher.Match(owner) {
				// 只保留 CDN IP
				if s.cidrMatcher.Contains(a.A) {
					newResp.Answer = append(newResp.Answer, a)
					log.Printf("保留 CDN IP: %s 属于域名: %s", a.A.String(), owner)
				} else {
					log.Printf("过滤非 CDN IP: %s 属于域名: %s", a.A.String(), owner)
				}
			}
		}
	}

	return newResp
}

// returnCDNARecords 直接返回 CDN 节点的 A 记录
func (s *Server) returnCDNARecords(req *dns.Msg, cdnIPs []net.IP) *dns.Msg {
	// 创建新的响应
	newResp := new(dns.Msg)
	newResp.SetReply(req)

	// 获取请求的域名
	domain := req.Question[0].Name
	qType := req.Question[0].Qtype

	// 只处理 A 记录查询
	if qType != dns.TypeA {
		return newResp
	}

	// 获取域名的 TTL 设置
	ttl := uint32(60) // 默认 60 秒
	for _, rule := range s.config.Domains {
		pattern := rule.Pattern
		if util.MatchDomain(pattern, strings.TrimSuffix(domain, ".")) {
			if rule.TTL > 0 {
				ttl = rule.TTL
			}
			break
		}
	}

	// 为每个 CDN IP 创建 A 记录
	for _, ip := range cdnIPs {
		a := new(dns.A)
		a.Hdr = dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}
		a.A = ip
		newResp.Answer = append(newResp.Answer, a)
		log.Printf("返回 CDN IP: %s 给域名: %s, TTL: %d", ip.String(), domain, ttl)
	}

	return newResp
}

// checkCache 检查缓存
func (s *Server) checkCache(r *dns.Msg) *dns.Msg {
	if len(r.Question) == 0 {
		return nil
	}

	key := r.Question[0].String()
	s.cache.mu.RLock()
	defer s.cache.mu.RUnlock()

	entry, found := s.cache.entries[key]
	if !found {
		return nil
	}

	// 检查是否过期
	if time.Now().After(entry.expireAt) {
		return nil
	}

	// 返回缓存的响应副本
	resp := entry.msg.Copy()
	resp.Id = r.Id
	return resp
}

// updateCache 更新缓存
func (s *Server) updateCache(req, resp *dns.Msg) {
	if len(req.Question) == 0 || resp == nil {
		return
	}

	key := req.Question[0].String()
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()

	// 如果缓存已满，清除一个随机条目
	if len(s.cache.entries) >= s.cache.maxSize {
		// 简单实现：删除第一个找到的条目
		for k := range s.cache.entries {
			delete(s.cache.entries, k)
			break
		}
	}

	// 添加到缓存
	s.cache.entries[key] = &CacheEntry{
		msg:      resp.Copy(),
		expireAt: time.Now().Add(s.cache.ttl),
	}
}

// OnConfigChange 实现 ConfigChangeListener 接口
func (s *Server) OnConfigChange(oldConfig, newConfig *config.Config) {
	log.Printf("配置已更新，正在应用新配置...")
	
	// 更新服务器配置
	s.config = newConfig
	
	// 更新 CIDR 匹配器
	s.cidrMatcher.Clear()
	s.cidrMatcher.AddCIDRs(newConfig.CDNIPs)
	
	// 更新域名匹配器
	s.domainMatcher.Clear()
	for _, rule := range newConfig.Domains {
		s.domainMatcher.AddPattern(rule.Pattern)
	}
	
	// 更新客户端超时设置
	s.client.Timeout = newConfig.Upstream.Timeout
	s.upstream = newConfig.Upstream.Server
	s.timeout = newConfig.Upstream.Timeout
	
	// 更新缓存设置
	s.cache.mu.Lock()
	s.cache.maxSize = newConfig.Server.CacheSize
	s.cache.ttl = newConfig.Server.CacheTTL
	s.cache.mu.Unlock()
	
	log.Printf("新配置已应用，CDN IP 数量: %d, 域名规则数量: %d", 
		s.cidrMatcher.Count(), s.domainMatcher.Count())
}
