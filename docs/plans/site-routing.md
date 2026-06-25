# Plan: sowerd Site Routing by SNI

状态：已实现；本文为非权威历史计划。权威规则见根目录 `ARCHITECTURE.md`。

## 背景与现状

`sowerd` 在 443 上对每条连接先探测 `sower` transport，不匹配时 fallback 到单一 `fake_site`：
- 若 `fake_site` 是本地目录：在 `127.0.0.1:80` 用 `http.FileServer` 服务，443 fallback 通过 `relay.RelayTo` 转发到 `127.0.0.1:80`。
- 若 `fake_site` 是 `host:port`：443 fallback 直接裸 TCP relay 到该地址。

该 fallback 语义是"单一伪装/兜底站点"，无法按访问域名路由到不同上游。

## 目标

将 443 的 fallback 从单一目标扩展为按 TLS SNI 路由：
- 配置若干 `site_routes`，每条映射一组精确域名到一个 upstream URL（`http://` 或 `https://`）。
- 命中某 route 的连接转发到对应 upstream。
- 未命中任何 route 的连接走配置的 `fake_site` 兜底；当 `fake_site` 是目录时，由本地 fileserver 服务。

## 非目标

- 不动 `sower` 认证流量的目标路由（由 transport 帧解码决定，与域名无关）。
- 不支持通配域名（`*.example.com`）。精确匹配即可。
- 不在 `sower` 客户端侧实现；本功能仅限 `sowerd`。
- HTTP（:80）请求只做重定向到 HTTPS，不参与 site routing。

## 设计

### 配置

`config/sowerd.go` 新增：

```go
type SiteRoute struct {
    Domains  []string // 精确域名，小写归一化
    Upstream string   // 完整 URL，如 http://127.0.0.1:8080 或 https://backend.example.com
}

type SowerdConfig struct {
    // ...现有字段...
    SiteRoutes []SiteRoute
    FakeSite   string // 保留，作为未命中时的最终兜底
}
```

`Validate` 约束：
- `Upstream` 必须是合法 URL，scheme 仅允许 `http`/`https`。
- `Domains` 非空，每项小写归一化，不含通配符 `*`，不重复。
- 同一域名不得出现在多条 route 中（重复即配置错误，启动失败）。
- `FakeSite` 语义不变：未命中 route 时使用；仍要求是已存在目录或 `host:port`。

示例（TOML）：

```toml
fake_site = "/var/www"

[[site_routes]]
domains = ["a.example.com", "b.example.com"]
upstream = "http://127.0.0.1:8080"

[[site_routes]]
domains = ["c.example.com"]
upstream = "https://backend.example.com"
```

### 匹配规则

- 构建 `map[string]*url.URL`：SNI → upstream。
- `handleConn` 中两个 transport 探测失败后，取 `(*tls.Conn).ConnectionState().ServerName`，小写归一化后查 map。
- 命中：用 `httputil.ReverseProxy` 转发到 upstream URL。
- 未命中：走现有 `fakeSite` 兜底（本地目录时 relay 到 `127.0.0.1:80` 的 fileserver）。

### 证书

- autocert 模式（未配置 `cert.cert`）：`autocert.Manager.GetCertificate` 已按 SNI 自动申请，多域名无需改动。
- 自定义 cert 模式：单一证书只覆盖配置的 SAN，多域名路由需自行提供覆盖所有域名的多 SAN 证书；否则非证书覆盖域名的 TLS 握手会失败。文档需声明此约束。

### 443 fallback 流程改动

当前 fallback 用 `relay.RelayTo(rereadConn, fakeSite)` 裸 TCP 转发。新流程：

1. transport 探测失败后，`rereadConn.Stop().Reread()` 重置字节流到起点。
2. 取 SNI。
3. 若 SNI 命中 route：
   - 构造 `httputil.NewSingleHostReverseProxy(targetURL)`。
   - 用单连接 listener 模式（把 `rereadConn` 包装成一次性 `net.Listener`）交给 `http.Server{Handler: reverseProxy}.Serve(listener)`，让标准库解析 HTTP 请求并反向代理。
4. 若未命中：保持现有 `relay.RelayTo(rereadConn, fakeSite)` 行为不变。

单连接 listener 模式：实现一个只 `Accept` 一次 `rereadConn`、之后阻塞直到连接关闭的 `net.Listener`，供 `http.Server.Serve` 消费。

### HTTP/2 限制

当前 `NextProtos = ["http/1.1", "h2"]`。fallback 的 reverse proxy 路径在 MVP 仅支持 HTTP/1.1（`http.Server` 默认不启用 h2，启用需额外 `http2.ConfigureServer`）。MVP 决策：将 `NextProtos` 改为 `["http/1.1"]`，简化 fallback 行为，避免协商到 h2 后 reverse proxy 不支持的错配。

影响：fallback/伪装站点仅 HTTP/1.1；`sower` transport 不走 ALPN，不受影响。

## 兼容性

- 未配置 `site_routes` 时，行为与当前完全一致（单一 `fake_site` 兜底）。
- `fake_site` 字段保留，语义不变：可以是本地目录，也可以是 `host:port`。

## 实现决策

- reverse proxy 会把 outbound `Host` 显式改写为 upstream Host，路径和查询参数保留。
- reverse proxy 会拒绝 HTTP upgrade 请求，避免 fallback 连接被 hijack 后生命周期不可控。
- reverse proxy 设置客户端 header、upstream dial、TLS handshake 和 upstream response header 超时。

## 实现清单

1. `config/sowerd.go`：新增 `SiteRoute`、`SiteRoutes`，`Validate`，示例配置字符串。
2. `config/sowerd_test.go`：覆盖 Validate 各分支。
3. `cmd/sowerd`：新增 site router（map + 查询）与单连接 listener。
4. `cmd/sowerd/main.go`：`handleConn` fallback 分支接入 router + reverse proxy；`NextProtos` 改为 `http/1.1`。
5. `cmd/sowerd/main_test.go`：SNI 命中/未命中/无 routes 兼容的集成测试（用本地 dummy upstream listener）。
6. `ARCHITECTURE.md`：定稿后回写 sowerd data flow 与 design decisions。
7. `docs/README.md`：索引更新。
