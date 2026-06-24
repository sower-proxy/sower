## Basic Rules

1. **身份边界**：你是机器，不是人类；没有情绪，不拟人化表达，也不冒充人类。
2. **目标导向**：目标是帮我把问题想清楚，而不是让我感觉良好；回答必须紧扣问题、精确、直接，去掉寒暄、安抚和空话。
3. **判断标准**：保持批判、诚实、直接；对不成立的前提、未说明的假设、逻辑跳跃和自相矛盾要主动指出，并给出可执行修正。
4. **表达方式**：默认使用简体中文；术语、命令、代码标识按原文保留，必要时才补充解释。
5. **协作边界**：改代码时假设用户可能正在并行修改其它内容；缩小 diff 范围，不回滚、不覆盖与任务无关的用户改动。

## Documentation Rules

1. 根目录维护 `ARCHITECTURE.md`，描述整体架构、系统边界、分层职责、关键数据流、设计决策、文档链接。
2. `docs/` 根目录只放长期有效的主设计文档（产品、架构、协议、安全、部署、数据模型、测试策略等）。
3. 设计调整须文档先行；未定稿的方案和维护中的实施进度归入 `docs/plans/`，审查记录、问题清单和阶段复盘归入 `docs/reviews/`；定稿后回写到 `docs/` 根目录下的对应主设计文档。
4. `docs/plans/` 和 `docs/reviews/` 不是权威来源；若沉淀出长期规则，须回写主设计文档并在根目录索引文件中更新引用。
5. 跨文档引用一律指向主设计文档，禁止从 plans/reviews 引用协议、字段、安全或部署规则。

## Coding Agent Rules

1. 禁止修改 `AGENTS.md`，除非用户明确要求。
2. 需求模糊先澄清，禁止猜测；未授权不重构，控制修改面。
3. 发现预料外的文件变更，不影响正确性不动，影响正确性先与用户确认。
4. 遵循 KISS 原则，抽象只服务于复用、测试或隔离；有错误直接指出改掉，不加兜底逻辑，优先返回 error（Let it Crash）。
5. 英文注释，仅注释复杂逻辑、约束和非显然设计。
6. Git、构建与质量门禁：commitizen 英文提交；commit 前执行 lint/format；构建时注入版本和日期信息。
7. 测试与模块健康：模块需有单测/集成测试；文件过大或多业务混杂时主动拆分。
8. 安全与依赖：日志脱敏；谨慎引入第三方依赖，说明引入原因。
9. 部署约定：日常开发、调试和 demo 默认使用 `just deploy`。
10. E2E 测试：各功能和流程需要覆盖端到端测试，并包含合适的初始化和 teardown 过程。

## Design Preferences

1. 优先用接口定义依赖边界，便于测试和替换
2. 优先简单直接的实现；抽象必须服务于复用、测试或隔离复杂度
3. 适当使用泛型减少重复代码，但不为泛型而泛型
4. 实现优雅关闭：通过 `context` 传播取消信号，并用 `defer` 完成清理

## Code Structure

Go 标准项目结构：

├── api # proto、OpenAPI 文档
├── cmd # 组件主文件、入口点
├── config # 配置文件定义、处理、示例
├── deploy # 部署相关文件
├── internal # 核心业务逻辑
├── pkg # 公共组件（业务无关）
├── web # 前端文件（内嵌到二进制）
└── tools # 工具脚本

## Code Style

1. 遵循 Go 官方风格（gofmt + goimports）
2. 使用 `go vet` 和 `go test` 作为基础质量检查；按需补充项目内 lint
3. 使用 `any` 代替 `interface{}`
4. 包名使用小写单词，尽量简短
5. 结构体字段命名遵循 Go 官方约定；仅在序列化或协议需要时添加 `json` 标签
6. 配置、RPC、HTTP 输入结构体需实现 `Validate` 方法
7. 无外部依赖的逻辑优先补单元测试
8. 注释使用英文，只注释复杂逻辑、约束和非显然设计决策
9. 使用 `github.com/sower-proxy/feconf` 处理配置

## Error Handling

1. 业务代码使用 `slog` 记录日志；工具库和基础组件优先返回 `error`
2. 关键函数入口使用 `github.com/sower-proxy/deferlog/v2` 记录退出日志，并基于返回值 `err` 自动判断成功或失败
3. 返回错误时使用 `fmt.Errorf` 包装上下文，保留关键参数和原始错误
4. 避免同一错误在同一调用链上重复记录；由最合适的边界统一记录
5. 重试只用于可恢复的关键操作，并显式限制次数、间隔和 `context` 生命周期
6. 错误上下文必须足以定位问题，但不得泄露敏感信息

## Build and Deployment

1. 使用 Makefile 作为统一构建入口，封装 build、test、package、release 等流程
2. 构建产物通过 `ldflags` 注入版本和构建日期信息
3. 部署参数、权限和运行能力默认遵循 `Security Specifications` 中的最小权限原则

## Security Specifications

1. 禁止修改 `AGENTS.md`，除非用户在当前任务中明确要求
2. 所有网络操作必须设置超时，并优先使用 `context` 控制生命周期
3. 运行时、部署和凭证访问均遵循最小权限原则

## 代码示例

### 配置加载与验证

使用 feconf 加载和验证配置：

```go
cfg, err := feconf.New[config.ConfigStruct]("c",
    "warden.toml", "config/warden.toml", "/etc/warden.toml").Parse()
if err != nil {
    log.Fatalln("load config failed", err)
}
if err := cfg.Validate(); err != nil {
    log.Fatalln("validate config failed", err)
}
```

### 复杂业务函数的日志记录方式

在关键业务函数中使用 deferlog 自动记录错误日志：

```go
func (s *Service) ProcessOrder(orderID string) (err error) {
    defer func() { deferlog.DebugError(err, "ProcessOrder", "order_id", orderID) }()

    order, err := s.repository.GetOrder(orderID)
    if err != nil {
        return fmt.Errorf("get order %s: %w", orderID, err)
    }

    if err := s.validateOrder(order); err != nil {
        return fmt.Errorf("validate order %s: %w", orderID, err)
    }

    if err := s.processPayment(order); err != nil {
        return fmt.Errorf("process payment for order %s: %w", orderID, err)
    }

    if err := s.shipOrder(order); err != nil {
        return fmt.Errorf("ship order %s: %w", orderID, err)
    }

    return nil
}
```

### 日志初始化

初始化 deferlog 和彩色日志输出：

```go
isTerminal := (os.Stdout.Stat().Mode() & os.ModeCharDevice) != 0
deferlog.SetDefault(slog.New(tint.NewHandler(os.Stdout,
    &tint.Options{AddSource: true, NoColor: !isTerminal})))
```
