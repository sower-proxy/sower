# Plan: sowerd Site Routing by SNI

状态：已实现；本文为非权威历史计划。权威规则见根目录 `ARCHITECTURE.md`，安全规则见 `docs/security.md`。

本文只保留已定稿设计里**不容易从代码复现的取舍记录**；配置字段、校验分支与运行流程以代码和 `ARCHITECTURE.md` 为准。

## 目标

将 443 的 fallback 从单一 `fake_site` 扩展为按 TLS SNI 路由：若干 `site_routes` 把一组精确域名映射到 upstream URL，未命中回落 `fake_site`。

## 非目标

- 不动 `sower` 认证流量的目标路由（由 transport 帧解码决定，与域名无关）。
- 不支持通配域名（`*.example.com`），只做精确匹配；拒绝理由见 `docs/security.md` 的 "Rejected Directions"。
- 不在 `sower` 客户端侧实现。
- HTTP（:80）不参与 site routing（拒绝理由见 `docs/security.md`）。

## 关键取舍

- **精确 SNI 匹配，不做通配**：SNI 路由是精确 map 命中，通配要么让所有子域塌到同一 upstream（失去按域分流的意义），要么在边缘最热路径上引入优先级歧义。
- **用 reverse proxy 而非裸 TCP relay**：只有交给 `http.Server` 解析 HTTP 才能拿到路径做前缀分流，并统一重写 outbound `Host` 与转发头。代价是每条连接多一层 HTTP 解析。
- **单连接 listener 模式**：把 `rereadConn` 包装成只 `Accept` 一次的 `net.Listener` 交给 `http.Server.Serve`，让标准库处理请求生命周期，而不是自写 HTTP 解析循环。
- **HTTP/1.1-only 是有意决策**：`NextProtos` 只留 `http/1.1`；advertise h2 后 WebSocket 需要 RFC 8441 extended CONNECT，stdlib 不支持。详细证据与 per-SNI ALPN 替代方案被拒的理由见 `docs/security.md` 的 "Rejected Directions"。
- **未配置 `site_routes` 时 `fake_site` 仍是唯一兜底目标**：字段语义不变（已存在目录或 `host:port`），SNI 分流表为空即永不命中。但兜底机制本身已随这轮改造变化，不再是原先的裸 TCP relay：探测到 HTTP/1.x 请求行时走反向代理（保留原始 `Host`、设置 edge 转发头），其余流量才裸 relay，且 `NextProtos` 只剩 `http/1.1`（见 `ARCHITECTURE.md` 的 sowerd Data Flow 第 13–14 步）。

## 已推翻的实现决策

- ~~reverse proxy 会拒绝 HTTP upgrade 请求~~ — **已推翻**：现行实现不做任何拒绝，`httputil.ReverseProxy` 在 101 时经 hijack 直通，`TestReverseProxyConnWebSocket` 与 `TestReverseProxyConnPathRoutedWebSocket` 均通过，见 `ARCHITECTURE.md`。
- reverse proxy 会把 outbound `Host` 显式改写为 upstream Host，路径和查询参数保留；并设置客户端 header、upstream dial、TLS handshake、upstream response header 超时。（更新 v1.7.1：route 可设 `authority = "client"` 保留客户端 authority，见 [docs/security.md](../security.md)；本条描述默认行为。）
