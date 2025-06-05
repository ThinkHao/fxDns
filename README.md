# fxDns

fxDns 是一个高性能的 DNS 代理服务，用于接收前端的 DNS 请求，转发给上游 DNS 服务器，并根据配置对返回结果进行处理。主要用于将特定域名的解析结果引导至公司的 CDN 节点。

## 功能特点

- 支持 DNS 请求的接收、转发和响应处理
- 支持检测 CNAME 解析结果是否包含 CDN 节点 IP
- 支持过滤非 CDN 节点的 CNAME 解析结果
- 支持直接返回 CDN 节点 IP 的 A 记录
- 支持高并发处理
- 支持配置文件热加载 (修改 `config.yaml` 后自动生效)
- 支持泛域名配置
- 支持 CIDR 格式的 CDN 节点 IP 配置
- 提供 `setup.sh` 脚本，方便在 Linux 系统 (支持 systemd) 上进行安装、更新和作为服务运行
- 通过 GitHub Actions 自动构建和发布多平台版本

## 安装

### 从 Release 包安装 (推荐 Linux 用户)

我们通过 GitHub Actions 自动构建和发布版本。您可以从项目的 [Releases 页面](https://github.com/hao/fxdns/releases) 下载最新的发布包 (例如 `fxdns-vX.Y.Z-linux-amd64.tar.gz`)。

下载并解压后，包内包含 `fxdns` 二进制文件、`config.yaml.example` 示例配置文件以及 `setup.sh` 安装脚本。

```bash
# 假设下载了 fxdns-v0.1.0-linux-amd64.tar.gz
tar -xzvf fxdns-v0.1.0-linux-amd64.tar.gz
cd fxdns-v0.1.0 # 进入解压后的目录

# 执行安装脚本 (需要 sudo/root 权限)
sudo ./setup.sh install
```

`setup.sh` 脚本会自动：
- 创建必要的用户和目录。
- 复制 `fxdns` 二进制文件到 `/usr/local/bin/`。
- 复制 `config.yaml.example` 到 `/etc/fxdns/config.yaml` (如果目标文件不存在)。
- 设置 `fxdns` 二进制文件具有绑定特权端口 (如 53) 的能力 (`setcap`)。
- 将 `fxdns` 安装为 systemd 服务，并设置为开机自启。
- 启动 `fxdns` 服务。

安装完成后，您可以使用以下命令管理服务：
```bash
sudo systemctl status fxdns
sudo systemctl start fxdns
sudo systemctl stop fxdns
sudo systemctl restart fxdns
```
日志文件通常位于 `/var/log/fxdns/`。

#### 更新服务
如果您下载了新版本的发布包，可以使用 `setup.sh` 进行更新：
```bash
# 进入新版本解压后的目录
sudo ./setup.sh update
```

#### 卸载服务
```bash
# 进入任意包含 setup.sh 的版本目录
sudo ./setup.sh uninstall
```

### 从源码编译 (开发者或非 Linux amd64/arm64 用户)

确保已安装 Go (推荐 1.20 或更高版本)，然后执行以下命令：

```bash
git clone https://github.com/hao/fxdns.git
cd fxdns
go build -o fxdns ./cmd/fxdns
```
编译完成后，`fxdns` 二进制文件将位于当前目录。

## 配置

配置文件使用 YAML 格式。
- 通过 `setup.sh` 安装后，配置文件位于 `/etc/fxdns/config.yaml`。
- 手动编译运行时，可以指定配置文件路径，默认为程序运行目录下的 `config.yaml` 或 `config/config.yaml` (取决于程序内部查找逻辑，建议明确指定)。

配置示例 (`config.yaml.example`):

```yaml
# 上游 DNS 服务器配置
upstream:
  server: "8.8.8.8:53" # 主上游 DNS
  fallback_server: "114.114.114.114:53" # 备用上游 DNS (可选)
  timeout: 5s

# 服务配置
server:
  listen: ":53" # 监听地址和端口
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
    ttl: 300 # 自定义此规则下记录的 TTL (可选)
  - pattern: "*.cdn.example.com"
    strategy: "return_cdn_a"    # 直接返回 CDN 节点 IP 的 A 记录
  - pattern: "static.example.org"
    strategy: "filter_non_cdn"
```

### 配置项说明

- `upstream`: 上游 DNS 服务器配置
  - `server`: 主上游 DNS 服务器地址，格式为 "IP:端口"。
  - `fallback_server`: (可选) 备用上游 DNS 服务器地址。当主服务器解析结果不符合特定条件时 (例如，CNAME 不含 CDN IP 且策略要求转发)，会使用此备用服务器。
  - `timeout`: 请求超时时间。

- `server`: 服务配置
  - `listen`: 监听地址，格式为 "IP:端口"，如 `":53"` 表示监听所有接口的 53 端口。
  - `workers`: 工作协程数量，用于控制并发。
  - `cache_size`: DNS 缓存大小（条目数）。
  - `cache_ttl`: DNS 缓存默认有效期。

- `cdn_ips`: CDN 节点 IP 列表，支持 CIDR 格式。用于判断解析结果是否指向 CDN。

- `domains`: 域名处理规则列表。
  - `pattern`: 域名模式，支持泛域名（如 `*.example.com`）。
  - `strategy`: 处理策略：
    - `filter_non_cdn`: 过滤掉解析结果 A 记录中非 CDN 的 IP 地址。
    - `return_cdn_a`: （此策略可能需要结合具体实现确认）通常意味着如果解析结果是 CDN IP，则直接返回；或者用于特定场景直接构造 CDN IP 的 A 记录。
    - (可能还有其他策略，请参考具体代码或更详细的配置文档)
  - `ttl`: (可选) 为符合此规则的 DNS 记录指定一个自定义的 TTL (Time To Live) 值。

## 使用方法 (手动运行)

如果您选择从源码编译并手动运行：

```bash
# 使用位于 /etc/fxdns/config.yaml 的配置文件 (如果存在)
# 或者在当前目录查找 config.yaml 或 config/config.yaml
./fxdns

# 指定配置文件启动
./fxdns -config=/path/to/your/config.yaml
```

## 注意事项

- **端口权限**: DNS 标准端口 53 是特权端口。
    - 如果使用 `setup.sh` 安装，脚本已通过 `setcap` 处理权限，服务能以非 root 用户运行并监听 53 端口。
    - 如果手动运行编译的二进制文件并希望监听 53 端口，您需要以 root 权限运行，或者手动为二进制文件设置 `sudo setcap 'cap_net_bind_service=+ep' ./fxdns`。
- **配置文件热加载**: 修改服务正在使用的配置文件 (`/etc/fxdns/config.yaml` 或手动指定的文件) 后，`fxDns` 会自动检测变更并重新加载配置，无需重启服务。
- **CDN IP 配置**: 确保 `cdn_ips` 列表准确且最新，以保证域名解析策略的正确性。

## 贡献

欢迎提交 Pull Requests 或 Issues。

## 许可证

MIT
