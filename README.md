# llm

模型客户端——多模型降级链/任务级预算/成本记账/分阶段模型路由/主备切换 —— oagentkit 模块矩阵 **tier 2**（集成层，依赖 tier0/tier1 + 重第三方地板）。

```
go get github.com/oagentkit/llm
```

| 包 | 职责 |
|---|---|
| `obsx` | eino callbacks 体系的 LLM 调用追踪 + token 用量采集 + 分阶段打标（自 `obs/obsx` 迁入 v0.12.0） |
| `llm` | 模型客户端：多模型降级链、任务级预算、成本记账、分阶段路由、主备切换 |
| `wrap` | 调用包装与限流适配 |
| `llmtest` | 模型层契约测试套件 |

## 定位

**tier 2** — 集成层，依赖 tier0/tier1 + 重第三方地板。

- 本仓依赖的矩阵内仓：`textx`。
  （`obsx` 已于 v0.12.0 从 `obs/obsx` 迁入本仓，`obs` 不再是外部依赖。）

## 外部依赖地板

- `github.com/cloudwego/eino`
- `github.com/meguminnnnnnnnn/go-openai`
- `github.com/cloudwego/eino-ext`
- `golang.org/x/time`

## 与 oekit 的关系

本矩阵（oagentkit，Agent/LLM 编排工具链）**依赖** oekit（Open Easy Kit，Go 微服务工具链）：
`oekit/pkg` / `oekit/concurrency` / `oekit/logx` 等。oekit 是另一套独立矩阵，不是子品牌。

## 开发

```bash
go test ./... -race
go vet ./...
gofmt -l .
```

规范见 [AGENTS.md](AGENTS.md)。

## License

MIT
- **降级链的每一跳都必须记录原因**（成本/限流/超时/内容策略），不得静默换模型。
- **预算判定必须在调用前**，不能事后扣减 —— 事后扣减等于没有预算。
- **panic 不能只转成 error。** `RunSafe*` 系列属于 `github.com/oekit/concurrency/async`，
  会把 panic 收进全局 `async.PanicHandler`（默认 `log.Printf`，进程入口可调
  `async.SetPanicHandler` 换成结构化日志），并以 `async.PanicError` 哨兵类型返回。
  本仓只用 `async.GoSafe` 系列，不在库内暴露 panic 处理注入点。
  要拿回结构化上下文，自己在 `recover` 分支里转成 `errors.Wrap`。