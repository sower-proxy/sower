# Plan: 拒绝自指代理目标（2026-10-12 r6c↔kr 递归）

状态：实施中（2026-10-12）。本文为非权威记录；长期规则回写 [ARCHITECTURE.md](../../ARCHITECTURE.md) 的 Design Decisions。

## 问题

2026-10-12 kr 上的 sowerd 被 OOM killer 杀掉（`anon-rss 969556kB`，1.6 GiB 主机），
当时它是唯一的大内存进程。触发链由另一会话定位（board 消息 `m-77507977-64f`，
finding `f-4d7d5bd2-cee`）：

- r6c 的 sower 监听 `100.64.1.1`（tailscale0）上的 80/443/53/1080；公网名
  `sower.wweir.fun` 的 A 记录就是 `100.64.1.1`。
- 任何指名该名字的代理请求到达 r6c 的 HTTP 代理监听器后，`cmd/sower/proxy.go` 的
  `handleHTTPConn`/`handleHTTPSConn` 无条件下发给 kr（显式代理路径不查规则）；
  kr 的 sowerd 解析同名得到 `100.64.1.1`（tailnet 内可达）并回 dial 进 r6c:80，
  r6c 再发下一次 —— **每一跳生成下一跳**。
- 实测：r6c 上 `100.64.1.1:80` 来自 kr（100.80.112.55）的 ESTABLISHED 连接
  155→254→305→495→601（约 5 分钟），触发它的 curl 退出后仍在增长；kr 上 sowerd
  同期 311 个 dial、649 个 fd。只有重启 r6c 的 sower 才停下。

## 不变式与修法

**代理拨号不得回到本机正在转发代理流量的监听器。** 只要目标（解析后）落在这组地址上，
转发它必然再次进入同一个代理，于是链自持。

实现（单一收口点，`router.DialProxyOnly`，显式代理与自动路由两条路径都经过它）：

- `Router.SetSelfProxyAddrs(addrs...)` 记录本进程转发代理流量的监听地址：每个 DNS 监听
  地址上的 `:80`、`:443`，加上 SOCKS5 地址（`cmd/sower/selfProxyAddrs`）。admin-only 与
  DNS 监听器不参与：它们不转发，拨过去不可能成链。
- `DialProxyOnly` 在拨号前调用 `selfTarget(domain, port)`：只有目标端口命中本机转发端口
  时才解析（否则直接跳过，零开销）；目标是 IP 字面量时直接比较；否则本地解析（远端解析在
  relay 侧发生，所以这里只需要知道"那个地址是不是我"），结果按 `selfProxyLookupTTL`
  （1 分钟）缓存，避免每条被代理连接都付一次 DNS 往返。
- 解析失败不视为自指证据：返回"无地址"，拨号照常进行，由 relay 报自己的解析失败。
- 拒绝错误是 `router.ErrSelfProxyTarget`：HTTP 返回 `403`，SOCKS5 回 `0x02`
  （connection not allowed by ruleset），HTTPS/SNI 路径关连接（该路径本来没有可写的响应）。

## 被否决的替代方案

- **只在 DNS 路径学习"指向自己的名字"**（收到 answer 是 serve IP 时记录该名字，拨号时查表）：
  零解析开销，但只有在名字经过本机 DNS 时才知道。用 tailscale MagicDNS / DoH / 自带缓存
  解析该名字的设备不会触发学习，链照样能起。不完整。
- **在 sower 协议帧里加跳数标记**：最通用（跨机环也能断），但改了帧格式与校验和，两端必须
  同时升级（见仓库 deploy skill 的协议不兼容警告），代价与风险远超本问题；本问题只需
  "不要拨回自己"。
- **给监听器加并发上界**：只把 OOM 变成"连接占满槽位"的另一种拒绝服务，不消除链本身。

## 验证

- `go vet ./...`、`go test ./...` 全绿；新增用例覆盖：IP 字面量自指、名字解析到自指、
  非自指（含未服务端口不解析）、未指定地址绑定（`0.0.0.0:443` 匹配任意本地地址）、
  解析失败放行、决策缓存（同名只解析一次）；`cmd/sower` 侧钉住 `selfProxyAddrs` 的集合。
- 现场：r6c 升级后，从 tailnet 发 `curl -x http://100.64.1.1:80 http://sower.wweir.fun/`
  应得 403、`--socks5-hostname` 应被规则拒绝，且两侧连接数不增长；正常代理（google 204、
  baidu 200）与 admin 控制台不受影响。

## 未决（不在本次范围）

- `relay.Relay` 只设读超时导致卡死连接永久泄漏（finding `f-1ae16668-b92`），需要在
  `sower-proxy/conns` 仓库修；sowerd 侧已有写超时缓解。
- r6c 客户端 1.37GB / 36 fd 的独立堆增长（finding `f-e7456e01-67c`），与连接保活无关。
