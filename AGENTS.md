# AGENTS.md — oagentkit/llm

> 模型客户端——多模型降级链/任务级预算/成本记账/分阶段模型路由/主备切换
> module：`github.com/oagentkit/llm` · org：[oagentkit = Open Agent Kit](https://github.com/oagentkit)
> 矩阵分层：**tier 2**（集成层，依赖 tier0/tier1 + 重第三方地板）

## 本仓包含

```
`/`（module 根，包名 llm → import `github.com/oagentkit/llm`）
`obsx/`  ← 自 obs 迁入（v0.12.0）：LLM 调用追踪 + token 用量 + 分阶段打标
`port/`  ← 窄接口层（**不引 eino**）：Complete(ctx, req) (string, error) 形态的
           降级链 / 任务级预算 / 成本记账；预算与成本内核由根包下沉而来，
           两条路径共用同一份实现（llm.CostTracker == port.CostTracker）
`wrap/`
`llmtest/`
```

> **包为什么住在 module 根（不要再挪回子目录）**
>
> 矩阵的 import 路径规则是 **`github.com/{org}/{仓名}/{包名末段}`**，中间层目录由**仓**承担、
> 一律丢弃。唯一的例外是**包名末段 == 仓名**：此时包直接落在 module 根，路径不重复末段。
>
> 本仓正是这一例——包名 `llm` 恰好等于仓名 `llm`，所以 import 是 `github.com/oagentkit/llm`
> 而不是 `github.com/oagentkit/llm/llm`。Go 的惯例本就是包名不重复 import 路径末段。
>
> 同仓的 `llmtest`、`wrap` 末段 ≠ 仓名，照常挂在仓下，路径不变。
> 改布局前先跑 `python3 matrix-tools/scripts/rewrite_imports.py --print-rules` 对权威映射表。

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

### 发版顺序

本仓的 `CHANGELOG.md` 由 `matrix-tools/scripts/gen_changelogs.py` 从 git 历史生成，
**不要手改**。发版顺序的**单一副本**见 org 规则仓
[CONTRIBUTING §发版](https://github.com/oagentkit/.github/blob/main/CONTRIBUTING.md#发版)：
**预写待发版段 → 提交 CHANGELOG.md → 在那个提交上打 annotated tag**。
tag 必须落在 CHANGELOG 提交**之后** —— 下游 `go get` 拿到的是 tag 快照，
快照里的 CHANGELOG.md 若缺自己那一段，发布说明就是残缺的。

⚠️ 无参重生成 CHANGELOG 会**吞掉待发版段**：生成器只按已存在的 tag 切段，
`--next-version` 是唯一能产出待发版段的开关。**预写之后、打 tag 之前，
不要跑无参重生成。**

打 tag 前自检依赖地板：内部 `require` 要钉在**打 tag 那一刻**各依赖仓的最新 tag
（`matrix-tools/scripts/verify_tag_floor.py`）。本仓已有 4 个 CHANGELOG-at-tag 欠账
（`llm@v0.12.0`/`v0.12.1`/`v0.13.0`/`v0.14.0`，清单见
`matrix-tools/scripts/changelog_tag_debt.json`）—— **反面例子，不是范例**。

## 仓特有纪律

- **降级链的每一跳都必须记录原因**（成本/限流/超时/内容策略），不得静默换模型。
- **预算判定必须在调用前**，不能事后扣减 —— 事后扣减等于没有预算。
- **panic 不能只转成 error。** `RunSafe*` 系列属于 `github.com/oekit/concurrency/async`，
  会把 panic 收进全局 `async.PanicHandler`（默认 `log.Printf`，进程入口可调
  `async.SetPanicHandler` 换成结构化日志），并以 `async.PanicError` 哨兵类型返回。
  本仓只用 `async.GoSafe` 系列，不在库内暴露 panic 处理注入点。
  要拿回结构化上下文，自己在 `recover` 分支里转成 `errors.Wrap`。

## 明确不属于本仓的

| 能力 | 归属 |
|---|---|
| 文本/差异/会话/报告 | `github.com/oagentkit/textx` |
| 事件总线/流控/子进程/热插拔/钩子 | `github.com/oagentkit/runtime` |
| 脱敏/出口围栏/权限/沙箱解析/审计 | `github.com/oagentkit/guardx` |
| DB 队列与选主 | `github.com/oagentkit/queue` |
| 通用 errors/stringx/encoding/mathx | `github.com/oekit/pkg` |
| goroutine 池/单飞/重试/令牌桶 | `github.com/oekit/concurrency` |
