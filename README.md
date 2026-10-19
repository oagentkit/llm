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

## 成本与定价

`llm` 自带**内建模型价表**，成本核算不必由每个下游各自维护一张表。

```go
tracker := llm.NewCostTracker()            // 用内建价表
tracker.RecordAuto("gpt-4o", "R1", 1200, 300)
```

**单位统一为「美元 / 每 100 万 token」**（`Price.InputPerM` / `OutputPerM`）。
历史上的 `Provider.CostPer1KTokens()` 与 `CostTracker.Record` 的 `costPer1K` 参数
是「每 1K」，两套单位并存容易错账一个数量级，故新 API 全部收敛到每 1M，
旧入口保留但标记 `Deprecated`。

### 查价与归一化

`PriceTable.Lookup` 按下列顺序取候选，命中即返回（命中方式对外可见，
见 `PriceLookup.Kind`）：

1. 原名精确命中；
2. 去掉**部署前缀**（取最后一个 `/` 之后）：`openai/gpt-4o` → `gpt-4o`；
3. 去掉**日期后缀**：`mimo-v2.5-pro-20250101` → `mimo-v2.5-pro`、
   `gpt-4o-2024-08-06` → `gpt-4o`。

**不做「最长前缀回落」**：那会把同主干异价 SKU 静默定价错（表里有 `gpt-4o` 时，
`gpt-4o-mini` 会按 gpt-4o 计费）。「静默算错」比「显式未知」更危险。

### 覆盖

```go
table := llm.NewPriceTable(map[string]llm.Price{
    "gpt-4o": {InputPerM: 1.25, OutputPerM: 3.5}, // 企业折扣价
})
tracker := llm.NewCostTrackerWithTable(table)
```

覆盖优先于内建价。契约价、代理加价、人民币计价合同的折算价，都走这条路径。
`Provider` 也可以实现 `llm.PricedProvider`（`ModelPricing() (Price, bool)`）显式给价，
它同样优先于价表。

### 「价格未知」不记 0

查不到价格时，**记录不会被丢掉，也不会被记成「成本 0 的正常调用」**：

- `CostRecord.PriceStatus` 取 `PriceUnpriced`（`CostUSD` 的 0 是占位，不是「免费」）；
- `CostTracker.Unpriced()` / `UnpricedCount()` 列出是哪些调用没计价；
- `ModelSummary.UnpricedCalls` 让汇总层也能区分：
  `TotalCostUSD == 0 && UnpricedCalls == 0` 才是真免费，
  `TotalCostUSD == 0 && UnpricedCalls > 0` 是**成本口径不完整**。

需要「查不到就硬失败」的出账链路用 `llm.LookupPriceStrict`（返回 `llm.ErrPriceUnknown`）。

### ⚠️ 价目表是参考价

内建价表是 **2026-10-20 采集的官方 list price 快照**：

- **厂商随时调价，本表必然滞后于厂商公告**——它不是权威报价；
- 同一模型在不同渠道（官方 / 代理 / 云托管）价格不同，缓存命中、批量、区域定价另算；
- 只收录**厂商以美元公布**的模型；人民币计价与阶梯计价的（通义/百炼等）不收——
  换算汇率会漂、阶梯价也无法用单一「每 1M 常数」表达，硬折算等于给出一个
  看起来精确的错误数字。这类模型请用覆盖。

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