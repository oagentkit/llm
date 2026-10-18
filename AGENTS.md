# AGENTS.md — oagentkit/llm

> 模型客户端——多模型降级链/任务级预算/成本记账/分阶段模型路由/主备切换
> module：`github.com/oagentkit/llm` · org：[oagentkit = Open Agent Kit](https://github.com/oagentkit)
> 矩阵分层：**tier 2**（集成层，依赖 tier0/tier1 + 重第三方地板）

## 本仓包含

```
`llm/`
`llm/wrap/`
`llm/llmtest/`
```

## 硬规则

### 分层与依赖

- 依赖严格自底向上，**禁止反向依赖**。本仓是 tier 2，只允许依赖 tier ≤ 2 的仓。
- 本仓依赖的矩阵内仓：`obs`、`textx`。
- 改 module 路径必须同步：全部 import、go.mod、README 安装行。

### 错误处理

- 一律使用 `github.com/oekit/pkg/errors`；业务错误用 BizError。
- 所有 error 禁止 `_` 忽略。

### 并发安全

- 禁止裸 `go func`：用 `github.com/oekit/concurrency/async` 的 GoSafe 系列（panic 被 recover）。
- 共享状态用 mutex / atomic 保护；包级全局可变状态用 `atomic.Pointer[T]`。

### API 设计

- 函数选项模式命名 `XxxOption`（禁止裸 `Option`，多包 co-import 会冲突）。
- 导出符号 PascalCase + godoc 注释；包文档放 `doc.go`。

### 日志

- 日志走 `oekit/logx`，组件 logger 用 `logx.Delegating("oagentkit-llm")`，
  禁止 `Default()`/`WithService` 声明包级 logger。
- 模型请求/响应落日志前必须脱敏（凭据与用户数据都可能出现在 prompt 里）。

## 仓特有纪律

- **降级链的每一跳都必须记录原因**（成本/限流/超时/内容策略），不得静默换模型。
- **预算判定必须在调用前**，不能事后扣减 —— 事后扣减等于没有预算。
- **panic 不能只转成 error。** `RunSafe*` 系列会把 panic 收进全局 `PanicHandler`
  （库不接 `SetPanicHandler`，默认 `log.Printf`）且返回的 `panic: X` 无哨兵类型，
  无法挂回领域上下文。用 `RunSafeErr` 前先确认丢掉的结构化上下文可接受。

## 明确不属于本仓的

| 能力 | 归属 |
|---|---|
| 文本/差异/会话/报告 | `github.com/oagentkit/textx` |
| 事件总线/流控/子进程/热插拔/钩子 | `github.com/oagentkit/runtime` |
| 脱敏/出口围栏/权限/沙箱解析/审计 | `github.com/oagentkit/guardx` |
| DB 队列与选主 | `github.com/oagentkit/queue` |
| 通用 errors/stringx/encoding/mathx | `github.com/oekit/pkg` |
| goroutine 池/单飞/重试/令牌桶 | `github.com/oekit/concurrency` |
