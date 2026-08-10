# Admin Console Plan (MVP)

Status: 已实现；本文为历史计划，非权威来源。权威规则见根目录 `ARCHITECTURE.md`。
Scope: `cmd/sower`（客户端）管理控制台，前端 embed 进二进制

## 目标

给 `sower`（客户端）加一个本地 admin 管理页面：

- 执行管理规则：查看 / 添加 / 删除 block、direct、proxy 三类路由规则（运行期生效）
- 监控流量：DNS 查询数、各入口连接数、上下行字节数、按域名聚合的流量统计
- 前端为 Svelte 5 + Vite（由 bun 驱动），构建产物 embed 进 Go 二进制

## 范围与边界（MVP 明确不做）

- **只做 `cmd/sower`，不做 `sowerd`**。sowerd 是服务端 TLS 入口，没有规则与流量管理面。
- ~~**规则变更仅运行期生效，重启后重置**，不改写 TOML、不持久化、不静默暗示持久化。~~ **已被 [`admin-persistence-config.md`](./admin-persistence-config.md) 取代**：规则变更改为增量持久化到 admin 状态文件，重启后重放；配置页提供全量展示与白名单字段（`log_level`、`dns.upstream`、`dns.fallback`）在线调整。
- **监控的是代理 payload 字节与请求/连接计数**，不是网卡级网络计量。
- 不做配置热加载、不做规则文件管理（文件仍走启动时加载路径）。

## 配置

`config/sower.go` 新增 `[admin]` 段：

```toml
[admin]
disable  = true                  # 默认禁用
addr     = "127.0.0.1:19090"     # 默认仅回环
password = ""                    # 为空时启动自动生成
```

校验规则：`disable=false` 时，`addr` 必须是合法 `host:port`；`password` 为空时启动自动生成一次性随机密码并打印到日志（已实现变更，见 `ARCHITECTURE.md`）。

## 后端设计

### 1. `router/ruleset.go` — 线程安全规则集

```go
type RuleSet struct {
    mu    sync.RWMutex
    rules []string          // 全部规则（配置内联 + 文件 + 运行期添加）的原始串
    set   map[string]struct{} // 去重键
    tree  *suffixtree.Node  // 由 rules 派生的匹配树
}
```

- `Match`（RLock）、`List`（返回拷贝）、`Count`、`Add`（O(1) 去重，追加 + 入树，不立即 GC）、`Compact`（GC）、`Remove`（从列表删除 + 从 rules 全量重建树，重建即 GC）
- 启动批量加载沿用现有模式：逐条 `Add`，结束时一次 `Compact`，避免大规则文件 O(n²) GC
- 删除时从**保留的完整 rules 列表**重建树，保证"配置/文件规则 + 运行期增删"内部一致
- 迁移 `Router.BlockRule/DirectRule/ProxyRule` 为 `*RuleSet`，`dnsRuleMatch` 的 `Match(string) bool` 接口兼容，测试调用点机械替换

### 2. `internal/admin/stats.go` — 流量统计

- 聚合原子计数：`dnsQueries`、`httpConns`、`httpsConns`、`socksConns`、`bytesUp`、`bytesDown`
- 有界 per-domain 表（上限 100000）：`domain → {conns, bytesUp, bytesDown, lastSeen, bySource, byClient}`；超限时按 lastSeen 批量淘汰（域名 1024/次、客户端 64/次），用固定大小堆单次扫描选出最旧批次，避免每插入一个新域名都全表扫描
- `WrapConn(conn) net.Conn`：**在协议解析前**包装客户端连接，先累计聚合字节（保证 HTTP 头 / TLS ClientHello 不被漏计），解析出域名后 `BindConn(conn, kind, domain)` 归属到域名并自增该域名连接数
- 方向语义（以客户端连接为参照）：从客户端读到 = 上行（bytesUp），向客户端写入 = 下行（bytesDown）
- DNS 计数：`RecordDNS(qname, clientIP)`，由 cmd/sower 的 `dns.Handler` 装饰器调用，**不给 router 加 admin 回调字段**
- `Snapshot()` 返回不可变快照：锁内仅复制标量与域名/客户端明细，排序与 Top-N 截断在锁外完成（Top-N 用固定大小堆，避免为只展示 100/50 项而全量排序），缩短锁持有时间以降低对流量记账的阻塞
- 默认视图（bytes + all + 无客户端筛选）的 SSE 快照由 server 缓存一个 tick（TTL 与 5s tick 相等），多个默认流共享一次快照计算与一次 history 采样；筛选视图仍即时计算，避免无界按客户端缓存

### 3. `internal/admin/server.go` — HTTP 服务

依赖注入接口（便于测试与替换）：

```go
type RuleManager interface {
    RuleList(category Category) ([]string, error)
    RuleAdd(category Category, rules ...string) error
    RuleRemove(category Category, rule string) (bool, error)
    RuleCount(category Category) uint64
}
```

API：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/session` | 密码换取会话；进程内随机 token + HttpOnly/SameSite=Strict cookie |
| DELETE | `/api/session` | 登出，吊销 token |
| GET | `/api/status` | version/date/uptime/规则计数/监听器状态 |
| GET | `/api/rules?category=block\|direct\|proxy` | 列出某类规则 |
| POST | `/api/rules` | `{"category":..., "rules":[...]}` 添加 |
| DELETE | `/api/rules` | 同上语义删除（幂等） |
| GET | `/api/traffic` | 聚合计数 + 按域名流量 TopN |
| GET | `/` 及 SPA 路由 | 静态资源（embed FS），未匹配路径回退 index.html |

安全约束：

- 密码**不落 localStorage**；认证后由 HttpOnly cookie 携带
- 会话 token：`crypto/rand` 32 字节 hex，服务端 map 持有，24h 过期，惰性清理
- 密码比对用 SHA-256 摘要后 `subtle.ConstantTimeCompare`（长度无关恒定时间）
- 变更类请求（非 GET/HEAD）校验 Origin 同源，不一致 403
- JSON body 限额 64KB，`DisallowUnknownFields` 严格解码，类别白名单校验，单次规则条数与单条长度受限
- 静态资源与 API 同 server，优雅关闭走 `http.Server.Shutdown`

### 4. `cmd/sower` 接线

- `proxy.go`：三个 handler（HTTP/HTTPS/SOCKS5）在协议解析前 `stats.WrapConn(conn)`，解析出域名后 `BindConn`
- DNS：`dnsStatsHandler{dns.Handler, *admin.Stats}` 装饰器包 `dns.Server.Handler`
- `startAdminListener(ctx, cfg, r, stats, errCh)`：`disable` 时直接返回；否则建 listener + `srv.Serve`，ctx 取消时 `Shutdown`
- 版本/日期经 Options 注入

## 前端设计（`web/`）

- **UI 层用 shadcn-svelte**：Tailwind CSS v4 + bits-ui + lucide-svelte，暗色主题（CSS 变量直接置为暗色值，不提供切换）。组件用 CLI 生成在 `src/lib/components/ui/`（button/card/input/textarea/label/badge/dropdown-menu/separator/table/tabs/alert）。已实现改为浅色主题（`main.ts` 移除 dark 类）。
- Svelte 5（runes）+ Vite，`bun install` / `bun run build` / `bun run check`（svelte-check）
- 视图：Login（Card + 密码 → POST /api/session）、Overview（卡片栅格）、Rules（按面包屑 category 上下文切换分类：列表 + 添加 + 删除）、Traffic（Table）
- **高级菜单面包屑**（参照 cyberguard 的 `AppBreadcrumbHeader`）：品牌 → 当前分区（DropdownMenu 下拉列出全部页面，带图标/描述/当前态）→ 动态上下文段（Rules 的 category 段可下拉切换 block/direct/proxy；Overview 的 version 段、Traffic 的 scope 段为静态段）；右侧收藏星标（localStorage 持久化）+ 页面菜单 + 状态徽章 + 退出。定位/键盘导航/外点关闭由 bits-ui DropdownMenu 承担。
- 有界轮询（3–5s），401 时回登录；loading/error/empty 三态齐全；不引图表库。已实现改为 SSE 实时流（`/api/stream`，会话续期/过期事件），图表用自研 `AreaChart`（`src/lib/components/AreaChart.svelte`）。
- `traffic.domains` 为空时服务端返回 `[]` 而非 `null`（Go nil slice 会序列化成 null，前端 `null.length` 会抛错）；前端同时做 `?? []` 防御
- `web/embed.go`：`//go:embed all:dist` 导出 `fs.Sub(FS, "dist")`
- 构建产物 `web/dist` **提交进仓库**（go:embed 要求编译期文件存在，占位符会产生"能编译但 admin 页面是坏的"的二进制）

## 构建

`Makefile`：

- `build: web sower sowerd`；`sower` 目标依赖 `web`（`bun install --frozen-lockfile` + `bun run build`），`sowerd` 不依赖
- 保留独立 `web` 目标；`.gitignore` 增加 `web/node_modules`、`web/dist` 的排除处理（dist 需提交，故不 ignore）

## 测试清单

- Go：`RuleSet` 并发 Add/Remove/Match（-race）；admin 认证 / 畸形 JSON / 非法 category / CRUD / 静态回退 / 关闭；stats 方向与字节核算 / 域名上限淘汰；config 校验
- 前端：`bun run check`、`bun run build`
- 全量：`go vet ./...`、`go test -race ./...`、`make build`
- 端到端：启动真实 admin listener，浏览器截图桌面/移动宽度验证渲染与控件无重叠

## 文档收尾

- 定稿后回写 `ARCHITECTURE.md`（admin 边界、数据流、设计决策）与 `README.md`（admin 用法）；`[admin]` 段示例以 `config/sower.toml` 为准
