package dns

import (
	// "errors" // 移除未使用的 errors 包
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hao/fxdns/internal/config"
	"github.com/hao/fxdns/internal/util"
	"github.com/miekg/dns"
)

// 备用上游从配置读取，不再使用硬编码常量

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
	mu            sync.RWMutex // 添加互斥锁
	shutdownChan  chan struct{} // 用于通知 ListenAndServe 协程停止
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

	server.shutdownChan = make(chan struct{}) // 初始化 shutdownChan
	return server, nil
}

// Start 启动 DNS 代理服务器并开始配置监控
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 启动配置监控
	if err := s.configManager.StartWatching(); err != nil {
		log.Printf("DNS Server: 启动配置监控失败: %v", err)
		return err
	}

	// 初始化并启动 miekg/dns 服务器
	return s.startDNSServerProcess()
}

// startDNSServerProcess 负责实际创建和启动 miekg/dns 服务器实例。
// 调用此方法时，调用者应持有 s.mu 的锁。
func (s *Server) startDNSServerProcess() error {
	cfg := s.config // 使用当前 Server 持有的配置

	// 如果已经有一个服务器在运行，先尝试关闭它 (理论上 Start 时不应该有)
	if s.server != nil {
		log.Println("DNS Server: 检测到已有服务器实例，将先关闭它...")
		if err := s.server.Shutdown(); err != nil {
			log.Printf("DNS Server: 关闭旧服务器实例失败: %v", err)
			// 继续尝试启动新的，但记录错误
		}
		s.server = nil
	}

	// TODO: 未来可以从 cfg.Server.Network 读取网络类型，如果该字段被添加
	// 目前 config.ServerConfig 中没有 Network 字段，所以默认使用 "udp"
	network := "udp" 

	dnsServer := &dns.Server{
		Addr:    cfg.Server.Listen,
		Net:     network, // 使用确定的 network 类型
		Handler: s, // Server 类型实现了 ServeDNS 方法
		NotifyStartedFunc: func() {
			log.Printf("DNS Server: 已成功在 %s (%s) 启动监听", cfg.Server.Listen, network)
		},
		// ShutdownTimeout: 5 * time.Second, // 移除：miekg/dns.Server 没有此字段
	}
	s.server = dnsServer

	// 在新的 goroutine 中启动服务器，以便 Start 可以返回
	go func() {
		log.Printf("DNS Server: 尝试在 %s (%s) 启动 miekg/dns 服务器...", cfg.Server.Listen, network)
		if err := s.server.ListenAndServe(); err != nil {
			// 检查是否是因为我们主动关闭导致的错误
			select {
			case <-s.shutdownChan:
				log.Printf("DNS Server: ListenAndServe 在 %s (%s) 正常关闭。", cfg.Server.Listen, network)
			default:
				log.Printf("DNS Server: ListenAndServe 在 %s (%s) 失败: %v", cfg.Server.Listen, network, err)
				// 这里可以考虑如何通知主程序启动失败，例如通过一个 channel
			}
		}
	}()

	return nil // Start() 本身返回 nil，表示启动过程已开始
}

// Stop 停止 DNS 代理服务器
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Println("DNS Server: 开始停止服务...")

	// 停止配置文件监控
	if s.configManager != nil {
		log.Println("DNS Server: 正在停止配置监控...")
		s.configManager.StopWatching()
		log.Println("DNS Server: 配置监控已停止。")
	}

	// 关闭底层的 miekg/dns 服务器
	if s.server != nil {
		log.Println("DNS Server: 正在关闭 miekg/dns 服务器...")
		// 通知 ListenAndServe 协程我们是主动关闭
		// 检查 channel 是否已经关闭，避免重复关闭
		select {
		case <-s.shutdownChan:
			// Channel 已经关闭
		default:
			close(s.shutdownChan)
		}

		if err := s.server.Shutdown(); err != nil {
			log.Printf("DNS Server: 关闭 miekg/dns 服务器失败: %v", err)
			// 即使 shutdown 失败，也继续标记服务已停止
		} else {
			log.Println("DNS Server: miekg/dns 服务器已成功关闭。")
		}
		s.server = nil
	} else {
		log.Println("DNS Server: miekg/dns 服务器未运行或已停止。")
	}

	log.Println("DNS Server: 服务已成功停止。")
	return nil
}

// ServeDNS 实现 dns.Handler 接口，处理 DNS 请求
func (s *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	// 获取工作池令牌
	<-s.workerPool
	defer func() {
		s.workerPool <- struct{}{}
	}()

	// 1. 检查缓存
	if cachedResp := s.checkCache(r); cachedResp != nil {
		log.Printf("缓存命中: %s", r.Question[0].Name)
		w.WriteMsg(cachedResp)
		return
	}
	log.Printf("缓存未命中: %s", r.Question[0].Name)

	// 2. 转发到主上游服务器 (s.upstream)
	initialResp, _, err := s.client.Exchange(r, s.upstream)
	if err != nil {
		log.Printf("转发请求到主上游 %s 失败: %v, 请求: %s", s.upstream, err, r.Question[0].Name)
		dns.HandleFailed(w, r)
		return
	}

	// 2.1 如果主上游没有返回任何 A/AAAA，根据域级覆盖或全局配置不回退且不做校验，直接返回主上游结果
	if s.noAorAAAA(initialResp) && s.shouldNoRecordNoFallback(r.Question[0].Name) {
		// 针对 return_cdn_a 且启用剔除的规则，移除对应 CNAME
		if effStrategy, domainForStrategy := s.effectiveStrategyForNoRecord(r, initialResp); effStrategy == config.StrategyReturnCDNA && s.shouldStripCNAMEWhenNoRecord(domainForStrategy) {
			cleaned := s.stripCNAMEsForDomain(initialResp, domainForStrategy)
			s.updateCache(r, cleaned)
			w.WriteMsg(cleaned)
			return
		}
		s.updateCache(r, initialResp)
		w.WriteMsg(initialResp)
		return
	}

	// 3. 检查主上游响应的 CNAME 解析结果是否包含我司 CDN IP
	//    checkCNAMEForCDNIP 会使用 s.upstream 解析 CNAME 记录
	cdnIPsFound, cdnIPsList := s.checkCNAMEForCDNIP(initialResp)

	var finalResp *dns.Msg

	if !cdnIPsFound {
		// 4. 我司 CDN IP 未在主上游的 CNAME 解析结果中找到，则固定转发给 fallbackUpstream
		questionName := ""
		if len(r.Question) > 0 {
			questionName = r.Question[0].Name
		}
		fallback := strings.TrimSpace(s.config.Upstream.FallbackServer)
		if fallback == "" {
			log.Printf("CDN IP 未在 %s 的 CNAME 解析结果中找到，且未配置备用上游。直接返回主上游响应。请求: %s", s.upstream, questionName)
			finalResp = initialResp
		} else {
			log.Printf("CDN IP 未在 %s (主上游) 的 CNAME 解析结果中找到。转发到 %s, 原始请求: %s", s.upstream, fallback, questionName)
			var RTT time.Duration
			finalResp, RTT, err = s.client.Exchange(r, fallback)
			if err != nil {
				log.Printf("转发请求到 %s 失败: %v, 请求: %s", fallback, err, questionName)
				dns.HandleFailed(w, r)
				return
			}
			log.Printf("从 %s 获取到响应, RTT: %v, 请求: %s", fallback, RTT, questionName)
		}
		// 根据需求第四点：“返回其解析结果”，所以不对 finalResp 进行 further processing
	} else {
		// 5. 我司 CDN IP 在主上游的 CNAME 解析结果中找到。使用 processResponse 处理 initialResp
		questionName := ""
		if len(r.Question) > 0 {
			questionName = r.Question[0].Name
		}
		log.Printf("CDN IP 在 %s (主上游) 的 CNAME 解析结果中找到。处理响应, 原始请求: %s", s.upstream, questionName)
		finalResp = s.processResponse(r, initialResp, cdnIPsList) // 注意：传入 cdnIPsList
	}

	// 6. 更新缓存并发送响应
	if finalResp != nil {
		s.updateCache(r, finalResp)
		w.WriteMsg(finalResp)
	} else {
		// Should not happen if logic is correct, but as a fallback
		dns.HandleFailed(w, r)
	}
}

// forwardRequest 将请求转发到上游 DNS 服务器
func (s *Server) forwardRequest(r *dns.Msg) (*dns.Msg, error) {
	resp, _, err := s.client.Exchange(r, s.upstream)
	return resp, err
}

// processResponse 处理 DNS 响应 (在已知我司 CDN IP 存在于原始解析路径中的情况下调用)
func (s *Server) processResponse(req, originalResp *dns.Msg, cdnIPsFromInitialCheck []net.IP) *dns.Msg {
	if len(req.Question) == 0 || originalResp == nil {
		return originalResp
	}

	// cdnIPsFromInitialCheck 是从 handleDNSRequest 传入的，已确认包含我司 CDN IP
	// 如果 cdnIPsFromInitialCheck 为空，则表示逻辑错误或 handleDNSRequest 调用不当
	if len(cdnIPsFromInitialCheck) == 0 {
		log.Printf("错误: processResponse 被调用，但 cdnIPsFromInitialCheck 为空。请求: %s", req.Question[0].Name)
		return originalResp // 返回原始响应以避免进一步错误
	}

	qName := req.Question[0].Name
	domainForStrategy := normalizeDomain(qName)
	strategy := s.config.GetDomainStrategy(domainForStrategy)

	// 如果请求的域名本身没有特定策略 (Filter/ReturnA)，检查其 CNAME 链中是否有域名配置了此类策略
	if strategy == config.StrategyNone { // If no specific strategy, or if strategy is explicitly 'none' (which implies forward)
		chain := NewCNAMEChain()
		chain.BuildFromResponse(originalResp) // originalResp 是来自主上游的响应

		foundOverrideStrategyInChain := false
		for domainInChain := range chain.domains {
			if s.domainMatcher.Match(domainInChain) { // 确保是我们关心的域名模式
				chainStrategy := s.config.GetDomainStrategy(domainInChain)
				if chainStrategy == config.StrategyFilterNonCDN || chainStrategy == config.StrategyReturnCDNA {
					strategy = chainStrategy
					domainForStrategy = domainInChain // 更新应用策略的域名为 CNAME 链中的域名
					log.Printf("策略应用于 CNAME 链中的域名 %s: %s (原始请求 %s)", domainForStrategy, strategy, qName)
					foundOverrideStrategyInChain = true
					break
				}
			}
		}
		// 如果遍历 CNAME 链后策略仍为 None，说明没有匹配到 Filter/ReturnA 策略
		// 根据单测期望：当检测到 CDN IP 时，默认执行过滤非CDN逻辑
		if !foundOverrideStrategyInChain && strategy == config.StrategyNone {
			log.Printf("CDN IP 存在于 %s 的解析中，但域名 %s (或其 CNAME 链) 无特定策略。默认过滤非CDN IP。", qName, domainForStrategy)
			return s.filterNonCDNIPs(originalResp, cdnIPsFromInitialCheck)
		}
	}

	// 根据最终确定的策略和从主上游获取的 cdnIPsFromInitialCheck 进行处理
	switch strategy {
	case config.StrategyFilterNonCDN:
		log.Printf("域名 %s (策略针对 %s) 策略: %s。使用 %d 个CDN IP过滤非 CDN IP。原始请求: %s", qName, domainForStrategy, strategy, len(cdnIPsFromInitialCheck), qName)
		return s.filterNonCDNIPs(originalResp, cdnIPsFromInitialCheck)
	case config.StrategyReturnCDNA:
		log.Printf("域名 %s (策略针对 %s) 策略: %s。使用 %d 个CDN IP直接返回 CDN A 记录。原始请求: %s", qName, domainForStrategy, strategy, len(cdnIPsFromInitialCheck), qName)
		return s.returnCDNARecords(req, cdnIPsFromInitialCheck)
	default:
		// 此路径理论上不应到达，因为 strategy 要么是 Filter/ReturnA，要么已在上一个if块中返回 originalResp
		log.Printf("域名 %s (策略针对 %s) 未匹配任何处理策略 (%s)，但CDN IP存在。返回原始上游响应。原始请求: %s", qName, domainForStrategy, strategy, qName)
		return originalResp
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

// noAorAAAA 判断响应中是否缺少所有 A/AAAA 记录
func (s *Server) noAorAAAA(resp *dns.Msg) bool {
    if resp == nil {
        return true
    }
    for _, ans := range resp.Answer {
        switch ans.Header().Rrtype {
        case dns.TypeA, dns.TypeAAAA:
            return false
        }
    }
    return true
}

// effectiveStrategyForNoRecord 计算在无 A/AAAA 时适用的策略与目标域名
func (s *Server) effectiveStrategyForNoRecord(req *dns.Msg, originalResp *dns.Msg) (string, string) {
    if len(req.Question) == 0 {
        return config.StrategyNone, ""
    }
    qName := req.Question[0].Name
    domain := normalizeDomain(qName)
    strategy := s.config.GetDomainStrategy(domain)
    if strategy == config.StrategyReturnCDNA {
        return strategy, domain
    }
    if strategy == config.StrategyNone {
        chain := NewCNAMEChain()
        chain.BuildFromResponse(originalResp)
        for d := range chain.domains {
            if s.domainMatcher.Match(d) {
                s2 := s.config.GetDomainStrategy(d)
                if s2 == config.StrategyReturnCDNA {
                    return s2, d
                }
            }
        }
    }
    return strategy, domain
}

// shouldStripCNAMEWhenNoRecord 判断某域名对应规则是否启用无记录时剔除 CNAME
func (s *Server) shouldStripCNAMEWhenNoRecord(domain string) bool {
    d := strings.TrimSuffix(strings.ToLower(domain), ".")
    for _, rule := range s.config.Domains {
        if util.MatchDomain(rule.Pattern, d) {
            return rule.StripCNAMEWhenNoRecord
        }
    }
    return false
}

// stripCNAMEsForDomain 在响应中移除与目标域名及其 CNAME 链相关的 CNAME 记录
func (s *Server) stripCNAMEsForDomain(resp *dns.Msg, domain string) *dns.Msg {
    if resp == nil {
        return resp
    }
    domain = normalizeDomain(domain)

    // 构建 CNAME 链映射
    cnameMap := make(map[string]string)
    for _, ans := range resp.Answer {
        if cname, ok := ans.(*dns.CNAME); ok {
            source := normalizeDomain(cname.Hdr.Name)
            target := normalizeDomain(cname.Target)
            cnameMap[source] = target
        }
    }

    // 收集需要剔除的域名集合：domain 及其链上所有目标
    toStrip := make(map[string]bool)
    current := domain
    for {
        toStrip[current] = true
        next, ok := cnameMap[current]
        if !ok || next == current {
            break
        }
        current = next
    }

    // 生成新的响应，过滤掉匹配域名集合的 CNAME 记录
    newResp := resp.Copy()
    newAns := make([]dns.RR, 0, len(resp.Answer))
    for _, rr := range resp.Answer {
        if cname, ok := rr.(*dns.CNAME); ok {
            src := normalizeDomain(cname.Hdr.Name)
            if toStrip[src] {
                continue
            }
        }
        newAns = append(newAns, rr)
    }
    newResp.Answer = newAns
    return newResp
}

// shouldNoRecordNoFallback 判断当前域名是否在“无 A/AAAA 时不回退”策略下生效
func (s *Server) shouldNoRecordNoFallback(domain string) bool {
    d := strings.TrimSuffix(strings.ToLower(domain), ".")
    for _, rule := range s.config.Domains {
        if util.MatchDomain(rule.Pattern, d) {
            if rule.NoRecordNoFallback != nil {
                return *rule.NoRecordNoFallback
            }
            break
        }
    }
    return s.config.Upstream.NoRecordNoFallback
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
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Println("DNS Server: 检测到配置变更，开始处理...")

	// 检查监听地址或网络类型是否发生变化 (当前只检查 Listen)
	// TODO: 如果未来 config.ServerConfig 支持 Network 字段，也需要检查 oldConfig.Server.Network vs newConfig.Server.Network
	listenChanged := oldConfig.Server.Listen != newConfig.Server.Listen

	// 更新核心配置指针总是需要的
	s.config = newConfig

	// 更新其他依赖配置的组件
	s.client.Timeout = newConfig.Upstream.Timeout
	s.upstream = newConfig.Upstream.Server
	s.timeout = newConfig.Upstream.Timeout

	s.cidrMatcher.Clear()
	if err := s.cidrMatcher.AddCIDRs(newConfig.CDNIPs); err != nil {
		log.Printf("DNS Server: OnConfigChange 更新 CIDR 匹配器失败: %v", err)
		// 根据策略，可能需要返回或标记服务为不稳定状态
	}

	s.domainMatcher.Clear()
	for _, rule := range newConfig.Domains {
		s.domainMatcher.AddPattern(rule.Pattern)
	}

	s.cache.mu.Lock()
	s.cache.maxSize = newConfig.Server.CacheSize
	s.cache.ttl = newConfig.Server.CacheTTL
	s.cache.mu.Unlock()

	log.Printf("DNS Server: 内部配置已更新。新监听地址: %s, 上游 DNS: %s, CDN IP 数量: %d, 域名规则数量: %d", 
		newConfig.Server.Listen, newConfig.Upstream.Server, len(newConfig.CDNIPs), len(newConfig.Domains))

	if listenChanged {
		log.Printf("DNS Server: 监听到地址从 '%s' 变为 '%s'。准备重启 DNS 服务...", oldConfig.Server.Listen, newConfig.Server.Listen)

		// 1. 关闭当前服务器 (如果正在运行)
		if s.server != nil {
			log.Println("DNS Server: OnConfigChange 正在关闭旧的 miekg/dns 服务器...")
			// 通知旧的 ListenAndServe 协程我们是主动关闭
			// 需要为新的服务器实例创建一个新的 shutdownChan
			currentShutdownChan := s.shutdownChan
			go func(sdChan chan struct{}) { // 在 goroutine 中关闭，避免阻塞 OnConfigChange
				select {
				case <-sdChan:
				default:
					close(sdChan)
				}
			}(currentShutdownChan)

			if err := s.server.Shutdown(); err != nil {
				log.Printf("DNS Server: OnConfigChange 关闭旧 miekg/dns 服务器失败: %v", err)
			} else {
				log.Println("DNS Server: OnConfigChange 旧 miekg/dns 服务器已关闭。")
			}
			s.server = nil
		}

		// 为新的服务器实例创建一个新的 shutdownChan
		s.shutdownChan = make(chan struct{})

		// 2. 使用新配置启动服务器 (startDNSServerProcess 内部会处理 s.server 的创建和 goroutine 启动)
		log.Println("DNS Server: OnConfigChange 正在使用新配置启动 miekg/dns 服务器...")
		if err := s.startDNSServerProcess(); err != nil {
			log.Printf("DNS Server: OnConfigChange 启动新 miekg/dns 服务器失败: %v", err)
			// 启动失败，可能需要一些错误处理逻辑，例如尝试恢复旧配置或标记服务为不健康
		} else {
			log.Println("DNS Server: OnConfigChange 新 miekg/dns 服务器启动流程已开始。")
		}
	} else {
		log.Println("DNS Server: 监听地址未更改，无需重启服务。配置已动态应用。")
	}
}
