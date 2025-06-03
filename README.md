# fxDns

fxDns 是一个高性能的 DNS 代理服务，用于接收前端的 DNS 请求，转发给上游 DNS 服务器，并根据配置对返回结果进行处理。主要用于将特定域名的解析结果引导至公司的 CDN 节点。

## 功能特点

- 支持 DNS 请求的接收、转发和响应处理
- 支持检测 CNAME 解析结果是否包含 CDN 节点 IP
- 支持过滤非 CDN 节点的 CNAME 解析结果
- 支持直接返回 CDN 节点 IP 的 A 记录
- 支持高并发处理
- 支持配置文件热加载
- 支持泛域名配置
- 支持 CIDR 格式的 CDN 节点 IP 配置

## 安装

确保已安装 Go 1.16 或更高版本，然后执行以下命令：

```bash
git clone https://github.com/hao/fxdns.git
cd fxdns
go build -o fxdns ./cmd/fxdns
```

## 配置

配置文件使用 YAML 格式，默认位置为 `config/config.yaml`。配置示例：

```yaml
# 上游 DNS 服务器配置
upstream:
  server: "8.8.8.8:53"
  timeout: 5s

# 服务配置
server:
  listen: ":53"
  workers: 10
  cache_size: 1000
  cache_ttl: 60s

# CDN 节点 IP 配置（支持 CIDR 格式）
cdn_ips:
  - "192.168.1.0/24"
  - "10.0.0.0/8"
  - "172.16.0.0/12"

# 域名处理规则
domains:
  - pattern: "example.com"
    strategy: "filter_non_cdn"  # 过滤非 CDN 节点 IP
  - pattern: "*.cdn.example.com"
    strategy: "return_cdn_a"    # 直接返回 CDN 节点 IP 的 A 记录
  - pattern: "static.example.org"
    strategy: "filter_non_cdn"
```

### 配置项说明

- `upstream`: 上游 DNS 服务器配置
  - `server`: 上游 DNS 服务器地址，格式为 "IP:端口"
  - `timeout`: 请求超时时间

- `server`: 服务配置
  - `listen`: 监听地址，格式为 "IP:端口"，如 ":53" 表示监听所有接口的 53 端口
  - `workers`: 工作协程数量，用于控制并发
  - `cache_size`: 缓存大小（条目数）
  - `cache_ttl`: 缓存有效期

- `cdn_ips`: CDN 节点 IP 列表，支持 CIDR 格式

- `domains`: 域名处理规则
  - `pattern`: 域名模式，支持泛域名（如 "*.example.com"）
  - `strategy`: 处理策略
    - `filter_non_cdn`: 过滤非 CDN 节点 IP
    - `return_cdn_a`: 直接返回 CDN 节点 IP 的 A 记录

## 使用方法

```bash
# 使用默认配置文件启动
./fxdns

# 指定配置文件启动
./fxdns -config=/path/to/config.yaml
```

## 注意事项

- 程序需要以 root 权限运行才能监听 53 端口
- 配置文件支持热加载，修改后会自动重新加载
- 确保 CDN 节点 IP 配置正确，避免误过滤

## 许可证

MIT
