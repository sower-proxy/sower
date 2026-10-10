# Admin Console Plan (MVP)

Status: 已实现；本文为历史计划，非权威来源。权威规则见根目录 `ARCHITECTURE.md`。
Superseded by: [admin-persistence-config.md](admin-persistence-config.md)（扩展了本文的 MVP 边界：规则持久化与配置页）。

原 MVP 计划的后端设计（`RuleSet`、流量统计、admin HTTP 服务、`cmd/sower` 接线、构建与测试清单）已全部落地，并回写到 `ARCHITECTURE.md` 的 Layer Responsibilities、sower Data Flow 第 11–12 步与 Design Decisions，此处不再重复。仅保留两条仍在生效、且容易从代码复现不出来的前端约束：

- **不要用 `shadcn-svelte add` 覆盖 `web/src/lib/components/ui/`**：registry 产物会重新引入 bits-ui，而项目坚持手维护原生 Svelte 5 组件。UI 为 Tailwind v4 + lucide-svelte 浅色主题。
- **`web/dist` 必须提交进仓库**：`go:embed all:dist` 要求编译期文件存在，用占位符会产出"能编译但 admin 页面是坏的"的二进制。
