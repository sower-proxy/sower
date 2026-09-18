# Plan: sowerd 主动探测加固（未知 SNI 默认证书 + h2）

状态：已定稿并回写（2026-09-18）。本文为非权威历史记录，权威安全规则见 [security.md](../security.md)。

实施结果摘要：

- (b) 未知 SNI 回退默认证书：已实现并部署（commit `b2b344e`）。白名单内错误原样暴露；白名单外/空 SNI 回退主域名证书并透传客户端能力（避免多余的 RSA 签发）。
- (a) fallback 路径 h2：实施前核查否决——stdlib `httputil.ReverseProxy` 无 RFC 8441 支持，x/net（≤v0.59）仅以 `GODEBUG=http2xconnect=1` 提供 extended CONNECT 且默认禁用（golang/go#71128）；按 SNI 区分 ALPN 的替代方案因静默失效风险被拒。详细证据见 [security.md](../security.md) 的 "Rejected Directions"。
