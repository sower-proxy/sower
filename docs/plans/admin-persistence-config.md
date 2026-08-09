# Admin 规则持久化与配置页设计

Status: 已实现并部署验证（2026-08-07）；本文为历史计划，非权威来源。权威规则见根目录 `ARCHITECTURE.md`。
Supersedes: `docs/plans/admin-console.md` 中的 MVP 边界「规则变更仅运行期生效，重启后重置，不改写 TOML、不持久化」
Scope: `cmd/sower` admin 控制台

## 后续演进（超出本文 v1 范围，已实现）

- **配置页可编辑白名单扩展**：v1 仅 `log_level`、`dns.upstream`、`dns.fallback` 三个 immediate 字段；后续扩展为指针型 overrides，区分「未提交」与「显式清空」，白名单字段覆盖远程代理、监听地址、规则来源、会话选项等，除 `log_level` 与 DNS 上游立即生效外均需重启。
- **进程重启端点**：`POST /api/restart` 在 Unix 上原地 `exec`（PID 不变，systemd 无感知）；不支持的平台返回错误而非假成功。
- **会话持久化选项**：新增 `admin.session_file` 说明、`admin.disable_session_persistence`、`admin.cookie_secure`；持久化改为 best-effort（磁盘失败不阻断登录、不残留有效登出会话）。
- **规则命中/未命中统计**：每规则命中计数与未命中规则的域名访问统计，由 router 观察者喂入有界内存表，供规则页排序与 miss 视图展示。

## 背景与问题

Admin 页面的规则增删目前只改内存 `RuleSet`，重启后丢失。用户需要：

1. 页面上的规则调整**重启后仍然生效**；
2. 一个**配置展示页**，并对少数字段支持**页面调整**。

### 已验证的约束（设计前提）

- 规则来源是「远程/本地文件 + 配置内联 `rules`」的合并：远程文件（如 16 万行 adlist）不可写，本地文件回写会被刷新覆盖且格式不可控。**只能存增量（delta），不能回写源文件。**
- `cmd/sower` 用 `aconfig` 加载配置，`Files` 列表首个存在的文件生效，**无多文件合并**；feconf 同样只取第一个可读 URI。**overlay TOML 方案不可行**，配置覆盖必须走类型化状态文件。
- `log_level` 当前未生效：`main.go` 的 tint handler 未传 `Level`，需先引入 `slog.LevelVar`。
- DNS `upstream`/`fallback` 存于 `router.dns`（互斥锁保护），upstream 列表 5 分钟自动重建，热切换可行。
- 已有先例：`admin.SessionFile`（默认 `/etc/sower/sessions.json`）采用 tmp+fsync+rename、0600、`MkdirAll 0700` 的原子写入模式，本设计沿用。

## 总体方案

一个 admin 私有状态文件（JSON）承载两类数据：**规则 delta** 与**白名单配置覆盖**。不回写 TOML、不回写规则文件、不做 TOML 合并。

```toml
[admin]
state_file = "/etc/sower/admin-state.json" # 新增；空字符串禁用持久化（退化为现状）
```

- 与 `session_file` 保持独立（会话是临时凭证，状态是数据，生命周期不同）。
- 文件由 admin 全权拥有：启动时校验，解析失败则重命名为 `admin-state.json.corrupt-<ts>` 并从空状态启动，日志告警。
- 逃逸开关：`--ignore-admin-state` flag（aconfig 原生支持），用于错误覆盖导致无法启动时恢复。

### 状态文件 schema

```json
{
  "version": 1,
  "revision": 42,
  "updatedAt": "2026-08-06T15:00:00Z",
  "rules": {
    "block":  { "add": ["**.example.com"], "remove": ["**.ad.com"] },
    "direct": { "add": [], "remove": [] },
    "proxy":  { "add": [], "remove": [] }
  },
  "config": {
    "log_level": "debug",
    "dns_upstream": "1.1.1.1",
    "dns_fallback": "223.5.5.5"
  }
}
```

- `revision` 单调递增，配置 PATCH 用它做乐观并发控制；规则操作也递增（同一文件串行写）。
- `config` 段只含被覆盖的字段；缺省表示无覆盖。密钥永远不入此文件（v1 密钥全部只读）。

## 规则持久化：即时自动保存

无「保存」按钮。现有 `POST`/`DELETE /api/rules`（JSON body 携带 `category` 与 `rules`）语义不变，增加持久化：

### delta 代数

基线 = 启动时从文件 + 配置内联加载的规则集。

| 操作 | 规则在基线 | 规则在 add | 规则在 remove | 结果 |
| --- | --- | --- | --- | --- |
| PUT add | 是 | - | 是 | 清除 tombstone（恢复基线规则） |
| PUT add | 是 | - | 否 | 无操作 |
| PUT add | 否 | 否 | - | 加入 add |
| PUT add | 否 | 是 | - | 无操作（幂等） |
| DELETE | 是 | - | - | 加入 remove（tombstone） |
| DELETE | 否 | 是 | - | 从 add 移除 |
| DELETE | 否 | 否 | - | 幂等 no-op（204，现状不变） |

### 启动顺序

1. 加载配置，应用 `config` 覆盖段（仅白名单字段，在构建 router 之前）；
2. 按现状加载文件/内联规则，得到基线（内存中保留基线副本，供 reset 使用）；
3. 应用 `remove` tombstone，再应用 `add`；
4. GC：tombstone 已不在基线、add 已在基线的条目视为过期，清除并回写状态文件（日志记录条数）。

### 写入与失败语义

- 单次操作：在 `StateStore` 内构造候选 delta → 原子写（tmp+fsync+rename、0600）→ 成功后才修改运行期规则集。**写盘失败则丢弃候选状态并向 API 返回 5xx，内存规则与已持久化状态都不变**——不静默退化为仅内存（与 session 的降级策略不同：规则是用户数据，静默丢失比报错更糟糕）。
- 所有变更串行经过 `internal/admin/state.go` 的 `StateStore`（单互斥锁），操作频率低，不做防抖，每次操作直接落盘。

### API 增补

```text
GET  /api/rules/changes           → 三类 add/remove 清单与计数（"自定义变更"汇总）
POST /api/rules/reset             → 清空指定分类 delta（body 带 `category`）或全部 delta（body 为 `{}` 或 `{"category":""}`）
DELETE/PUT 现有端点不变
```

### 前端（规则页）

- 每次变更显示瞬时状态：`保存中…` → `已保存` / `保存失败`（失败时规则行恢复原状并报错）；
- 删除操作给出 **Undo**（提示条内 6 秒撤销 = 重新 POST）；
- 分类卡片头部显示「自定义变更 +3 / −1」badge，点击展开清单，提供「重置本类」。

## 配置页：全量展示 + 白名单调整

### 展示（v1 全部字段只读为主）

`GET /api/config` 返回生效配置，分组：运行（版本/构建日期/log_level）、远程代理、DNS、监听（socks5/admin 地址与状态文件）和规则来源。

每个字段带元数据：

```json
{
  "key": "dns.upstream",
  "value": "8.8.8.8",
  "editable": true,
  "applyMode": "immediate",        // immediate | restart | readonly
  "source": "override",            // config | default | override
  "constraint": "IPv4/IPv6 address"
}
```

**密钥不返回值**，只返回 `configured: true` 元数据（`remote.password`、`admin.password`）。

### 可编辑白名单（v1）

| 字段 | applyMode | 前置改造 |
| --- | --- | --- |
| `log_level` | immediate | `main.go` 引入 `slog.LevelVar` 接入 tint handler（当前 log_level 未生效，属顺手修复） |
| `dns.upstream` | immediate | `router` 新增 `SetDNS(upstream, fallback string)`：加锁更新字段、失效 refresh 定时、触发 upstream 重建 |
| `dns.fallback` | immediate | 同上 |

其余字段（监听地址、disable 开关、TLS、规则源 URL、所有密钥）v1 全部只读，元数据标注 `restart` 或 `readonly`。密码轮换需要独立流程与会话失效语义，不在本期。

### 修改流程

与规则的自动保存不同，配置修改走**显式应用**：

```text
PATCH /api/config
{ "revision": 42, "changes": { "log_level": "debug", "dns_upstream": "1.1.1.1" } }
→ 409 on revision mismatch; 400 with field errors on invalid
→ 成功：校验 → 落盘 → 应用到运行时 → 返回新 revision 与生效值
```

前端：字段行内编辑 → 底部出现变更摘要（diff：旧值 → 新值）→「应用更改」二次确认 → 提交。校验失败逐字段标红。

## 安全

- 状态文件 0600、目录 0700；密钥永不写入状态文件、永不出现在 API 响应与日志；
- 日志只记录 revision 与字段名，不记录值中的敏感部分；
- 所有写入先校验后落盘；启动时覆盖值非法 → 忽略该字段并告警（不阻断启动），`--ignore-admin-state` 可整体跳过；
- API 全部在既有 session 认证之后；PATCH 有 body 大小限制与 `DisallowUnknownFields`。

## 测试

- delta 代数：上表全部分支（含幂等 PUT、tombstone 恢复）；
- 重启恢复：基线 + delta → 运行时规则集等于预期；
- GC：过期 tombstone/add 清除并回写；
- 原子写失败：注入写盘错误 → API 5xx 且内存回滚；
- 并发：-race 下并发 PUT/DELETE/PATCH 串行化正确；
- PATCH：revision 冲突 409、非法值 400、未知字段 400、密钥字段不出现在 GET 响应；
- reset：单类/全部 reset 后规则集回到基线；
- 启动：损坏状态文件 → quarantine + 空状态启动；`--ignore-admin-state` 跳过。

## 实施顺序

1. `internal/admin/state.go`：StateStore + 原子写 + schema（独立可测）；
2. 规则持久化接通（RuleManager 适配层 + 启动顺序 + GC）+ 规则页保存状态/Undo/变更汇总；
3. `slog.LevelVar` 接线 + `router.SetDNS`；
4. `GET/PATCH /api/config` + 配置页（展示先行，编辑随后）；
5. 文档：定稿后回写 `docs/plans/admin-console.md` 的范围说明（或沉淀为主设计文档）。
