# sowerd 主动探测加固：未知 SNI 默认证书 + fallback 路径 h2

状态：实施中（未定稿，定稿后回写 `docs/` 主设计文档）

日期：2026-09-18
决策背景：用户明确「抗探测优先」，否决 QUIC/HTTP3（自定义 ALPN 会向主动探测者暴露私有协议存在）。

## 问题分析

sowerd 的 443 端口当前有两处与真实网站不符的行为，构成主动/被动探测特征：

1. **未知 SNI 在 TLS 握手阶段失败**。autocert 的 `HostPolicy` 白名单（site_routes 域名 + `cert.domains`）之外的名字拿不到证书，握手直接失败。真实网站普遍回退默认 vhost 证书完成握手。探测者用任意 SNI 一探便知「这个 443 对未知域名不正常」。
2. **ALPN 只声明 `http/1.1`**。2026 年真实网站几乎都会协商 h2；ServerHello 中选中的 ALPN 是明文可见的指纹。sowerd 的 fake-site / site-routes 反代却只走 HTTP/1.1。

作为对比，已被否决的方向：QUIC/HTTP3（`quic-go` + 自定义 ALPN）会把「存在非标准协议」这一事实直接交给 ALPN 枚举，比现状更差。

## 方案

### (b) 未知 SNI 回退默认证书

`buildTLSConfig` 中用公开 API 包装 `autocert.Manager.GetCertificate`：白名单内正常签发；白名单外/无 SNI 时，改用主域名（`certIssueDomains` 第一个）再取一次证书作为回退。主域名证书已缓存在内存（或触发一次签发），失败则返回原始错误。静态证书模式（`cert.cert` 已配置）本来就对所有 SNI 回同一张证书，无需改动。

效果：探测者对任意 SNI 都能完成握手并看到一张真实证书，随后进入与正常访客完全相同的 fake-site/site-routes 路径。

### (a) fallback 路径支持 h2 —— 已否决（2026-09-18 核查后撤回）

初稿计划 advertise `h2` 并在每连接 fallback 上用 `x/net/http2` 服务，实施前两项核查均不通过：

1. **stdlib `httputil.ReverseProxy` 不支持 RFC 8441**：`upgradeType()` 只认 `Connection: Upgrade`/`Upgrade` 头；extended CONNECT（`:protocol: websocket`，无 Upgrade 头）会被当普通 CONNECT 透传，hub 不是 CONNECT 代理，WSS 必断。
2. **x/net 到 v0.59 仍无程序化开关**：extended CONNECT 只能 `GODEBUG=http2xconnect=1` 进程级打开，且默认禁用的官方理由正是服务端 websocket 栈不支持会出问题（golang/go#71128）。

剩余唯一不破坏 WSS 的 h2 方案是按 SNI 区分 ALPN（WingGate 域名保持 http/1.1），但它把「新增 WS 路由必须记得设 h1_only」变成静默失效点，抗审查边缘组件不可接受；且「ALPN 只协商 http/1.1」本身是弱信号（大量真实小站如此），收益不值得。`NextProtos` 维持 `["http/1.1"]`。

### 实施结果

仅实施 (b)：`buildTLSConfig` 在 autocert 模式下用 `getCertificateWithFallback` 包装 `GetCertificate`——白名单内域名走原路径（错误原样暴露，绝不拿错名证书掩盖）；白名单外/空 SNI 回退到主域名证书，**透传原 hello 的客户端能力**（否则 autocert 会按非 ECDSA 客户端走 `domain+rsa` 缓存键，触发多余的 RSA 签发）。单测覆盖：未知/空/白名单 SNI 的回退与正常路径、签发错误不被掩盖。

## 风险与缓解

| 风险 | 缓解 |
|---|---|
| 回退证书掩盖白名单域名的真实错误（ACME/缓存失败） | 只对白名单外 SNI 回退；白名单内错误原样暴露（单测 `TestGetCertificateFallbackNeverMasksIssuanceErrors`） |
| 回退 hello 丢失客户端能力 → autocert 误判非 ECDSA → 多余 RSA 签发 | 透传原 hello 只改 ServerName |
| 探测者现在总能完成 TLS 握手，扫描流量增加 | 与真实站点行为一致；fallback 内容本身就是真网站 |
| h2 若未来重提，WSS-over-h2 链路（ReverseProxy 无 RFC 8441）断裂 | 已记录否决证据，重提前必须解决 stdlib 缺口 |

## 验证

单测：未知/空/白名单 SNI 的证书回退与正常路径、签发错误不被掩盖；既有 0x80/fake-site 测试全绿（`go test ./...` exit 0，13 包）。
部署 kr 后：`curl` h1 WSS 握手 101；`openssl s_client -servername <随机域名>` 完成握手并看到回退证书；`journalctl -u sowerd` 无新增 `proxy upstream error`；r6c sower e2e 不回归。

## 明确不做

- QUIC / HTTP3 传输（理由见上）。
- 连接复用/多路复用以降低统计特征（收益不确定，复杂度高，与 KISS 冲突）。
