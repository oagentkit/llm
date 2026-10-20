# llm CHANGELOG

> 本文件由 `matrix-tools/scripts/gen_changelogs.py` 从 git 历史生成，按 tag 切段、取每段内的 conventional commit 渲染。

## [v0.14.0] — 2026-10-20

### 新增(port)：窄接口层——不依赖 eino 的降级链/预算/成本

## 根因
本仓全部对外能力都长在 eino 的类型上：
    func NewClient(m model.BaseChatModel, modelName string, budget TokenAccountant) *Client
    func (c *Client) Generate(ctx, stage, msgs []*schema.Message) (*schema.Message, error)
非 eino 生态的宿主想接一个**单方法**窄接口（如
`Complete(ctx, req LLMRequest) (string, error)`），只能自己重写：拼请求体、
解析响应、判错误码、算超时、处理 JSON 模式。代价不只是代码量，还有**边界易错**
——HTTP 状态码判错就会静默把失败当成功（issue llm#11 里两个下游就在各写一遍）。
## 改动
### 1) 新增 `port` 包（**不引 eino**）
    type Completer interface {
        Complete(ctx context.Context, req Request) (string, error)
    }
    chain := port.NewChain(
        port.WithCompleter("gpt-4o", myCompleter),   // 先注册者优先
        port.WithCompleter("deepseek-chat", backup),  // 失败切下一环
        port.WithBudget(port.NewBudget(100000)),     // 预算耗尽 → 调用前拦下
        port.WithTracker(port.NewCostTracker()),     // 按价表计价并记账
    )
    out, err := chain.Complete(ctx, port.Request{Prompt: "…", Stage: "R1"})
降级链 / 任务级预算 / 成本记账在窄接口路径上**真的生效**，不是只把请求转发出去：
- 预算判定在**调用前**（事后扣减等于没有预算），耗尽则**一次都不调用**模型；
- 失败按错误分类重试或切下一环，每环节独立熔断分桶；
- 成本按内建价表记账，价表未收录的模型记为**未定价**（不记 0）；
- 另附 `ClassifyStatus`（按 HTTP 状态码判可重试），免得每个宿主各写一遍、各判错一遍。
### 2) 无 eino 的内核下沉到 `port`，根包用**类型别名**保持 API 不变
预算 / 成本 / 价表 / 熔断的实现**本来就不依赖 eino**，却原先住在根包 `llm` 里，
而根包因为 client.go 依赖 eino 而「整包不可用」——宿主连「记一笔 token」都用不上。
下沉后：
- 根包 `llm.Budget` / `llm.CostTracker` / `llm.Price` / `llm.PriceTable` 等
  全部是 `port.X` 的**类型别名**（`=`），**对外 API 一字未变**，调用方的类型断言
  与转换全部照旧生效；
- 两条路径**共用同一份**预算/成本/价表实现 ⇒ 口径完全一致，不会出现两套算账；
- `providerPrice` 留在根包（它依赖 eino 耦合的 `Provider` 接口）。
## 依赖面证明（不引 eino）
    $ go list -deps ./port | grep -c cloudwego/eino
    0
    $ go list -deps ./port | grep -E '^[a-z0-9.-]+\.[a-z]+/' | sort -u
    github.com/oagentkit/llm/port
    github.com/oagentkit/textx/textutil
    github.com/oekit/concurrency/breaker
    github.com/oekit/concurrency/window
    github.com/oekit/pkg/stringx
即：标准库 + oekit/concurrency（熔断）+ textx（token 估算），**无 eino、无 go-openai、
无 sonic**。
## 验收（假模型，不需要真网络）
- `TestChainFallsBackToNextCompleter` —— 首环节失败 → 切到备选；成本记在**实际执行的模型**上
- `TestChainBudgetExhaustedRejected` —— 预算耗尽 → 返回 ErrBudgetExhausted，且**模型一次都没被调用**
- `TestChainBudgetAccumulates` —— 成功调用后预算累计消耗
- `TestChainRecordsCost` —— 成本 == 价表单价 × token 数（关系断言，不写死数值）
- `TestChainUnknownModelUnpriced` —— 未知模型记「未定价」，不记 0
- `TestChainNonRetryableSkipsRetries` / `TestChainContextCancelNotBreaker` / `TestClassifyStatus` 等
`GOWORK=off go test ./...` 全绿。
## 文档
- `port/port.go` 包文档：为什么需要、依赖面、**怎么选（窄接口 vs eino 原生）**
- `README.md`：新增「窄接口层」章节 + 包表补 port
- `AGENTS.md`：包清单补 port
关联 issue oagentkit/llm#11

### 修复(port)：去掉测试桩里没用上的 usage 字段（lint: field usage is unused）

精确计量的覆盖走 WithTokenCounter 选项，桩里的 usage 字段没人用。

## [v0.13.1] — 2026-10-20

- [round-1] fix(go): Go 地板降到 go 1.24.0，CI go-version 同步到 1.24
- [round-1] fix(deps): 依赖地板抬到发版前已发布的最新（textx v0.6.0 / oekit/pkg v0.17.0），并修 README 与 go.mod 矛盾

## [v0.13.0] — 2026-10-20

### 新增(pricing)：内建模型价表 + 「价格未知」不再静默记 0

成本追踪能力本来是完整的（CostTracker/CostRecord），但**价格不在这一层**：
CostTracker.Record 要求调用方每次传入单价，llm 自己既不持有「模型→价格」映射，
也没有按模型名查价的入口。于是每个下游都得自己维护一张表（通常在应用配置里，
键是模型名精确匹配），漏配就静默记 0 —— 0 成本在账单上与「免费模型」不可区分。
改动：
1) 新增 pricing.go：内建价表 + 归一化查价 + 调用方覆盖
   - PriceTable / NewPriceTable(overrides) / DefaultPriceTable / LookupPrice /
     LookupPriceStrict / ErrPriceUnknown
   - 归一化规则（README 与 Lookup 文档同源）：原名 → 去部署前缀 → 去日期后缀
   - **不做最长前缀回落**：那会把同主干异价 SKU 静默定价错（gpt-4o-mini 按
     gpt-4o 计）。「静默算错」比「显式未知」更危险。
2) 单位统一为「美元 / 每 1M token」。仓里此前并存两套单位
   （Price 是每 1M，Provider.CostPer1KTokens 与 Record 的 costPer1K 是每 1K），
   差一个数量级。新 API 全部收敛到每 1M，旧入口保留但标记 Deprecated。
3) 「价格未知」不记 0：CostRecord 增 PriceStatus/PriceKey/PriceMatched；
   CostTracker 增 RecordAuto/RecordPriced/RecordUnpriced/Unpriced/UnpricedCount；
   ModelSummary 增 PricedCalls/UnpricedCalls。判读口径：
     TotalCostUSD==0 && UnpricedCalls==0  → 真免费
     TotalCostUSD==0 && UnpricedCalls>0  → 成本口径不完整（不是免费）
   需要硬失败的出账链路用 LookupPriceStrict（ErrPriceUnknown）。
4) 接入生产路径：Provider 侧新增可选接口 PricedProvider（显式单价，优先于价表）；
   Resilient 增 PriceTable 字段，buildClient 的记账改为
   providerPrice(p, table) —— Provider 显式价 > per-1K 非零 > 价表按模型名查 >
   未知。此前这条路径恒记 0（Provider 不给价时 costPer1K 为零值）。
5) README 增「成本与定价」章节：单位、归一化规则、覆盖方式、未知价的判读口径，
   以及**价目表时效性声明**（2026-10-20 采集的官方 list price 快照，必然滞后于
   厂商公告；只收美元计价模型，人民币/阶梯计价的请覆盖——硬折算等于给出一个
   看起来精确的错误数字）。
验证：
- GOWORK=off go test -race ./... 全绿
- 新增用例 14 个（含子用例）：已知模型按关系算价、未知模型**不记 0**且可查询、
  汇总区分「零成本」与「价格未知」、覆盖价优先、别名后缀可查、Provider 单价优先级、
  Resilient 生产路径端到端
- 单测断言一律用**关系**（查表单价 × token 数 == 记录成本），不写死价目表数值——
  价目表随厂商调价变动，写死数字会随表更新假失败
关联 issue oagentkit/llm#9

## [v0.12.1] — 2026-10-20

### 修复(docs)：panic 机制归属修正为 oekit/concurrency/async，SetPanicHandler 结论与事实相反

Co-authored-by: Claude Opus 4.8 (1M context) <noreply@anthropic.com>

## [v0.12.0] — 2026-10-20

### 新增(obsx)：接收 obs/obsx —— LLM 调用观测与模型客户端住在一起

自 github.com/oagentkit/obs/obsx 迁入（obs v0.9.0 起该路径退役）。
obsx 是 eino callbacks 适配层（绑框架），而本仓早已 import 它做成本记账与
阶段打标 —— 同一关注点，两个包住一起更顺。迁出后 obs 仓的 eino 依赖消失，
langfuse / audit 两个轻包不再被白拖 27 个第三方模块。
包名与语义不变（LLM 调用追踪 + token 用量采集 + 分阶段打标），
只是 import 路径从 github.com/oagentkit/obs/obsx 变为
github.com/oagentkit/llm/obsx。
关联 oagentkit/guardx#3

## [v0.11.0] — 2026-10-19

- 杂务(deps)：依赖地板对齐远端最新（obs v0.7.0 / textx v0.5.0） (13)

## [v0.10.0] — 2026-10-19

- 杂务(deps)：依赖地板对齐远端最新（obs v0.5.0 / textx v0.4.0 / concurrency v0.17.1 / pkg v0.15.1 / logx v0.7.1）

## [v0.9.0] — 2026-10-19

### 重构(llm)!：包上移至 module 根，import 路径不再重复末段

BREAKING CHANGE: import 路径 github.com/oagentkit/llm/llm → github.com/oagentkit/llm。
包名 llm 恰好等于仓名 llm，留在 llm/ 子目录会产出 stutter 的公开路径
（Go 惯例：包名不重复 import 路径末段）。
矩阵 import 规则随之收敛：matrix_paths.new_import_path 在「包名末段 == 仓名」时
收口到 module 根，不再拼末段。这是该规则的**唯一例外**，写在唯一构造点里，
gen_versions / gen_llms_txt / rewrite_imports / test_matrix_paths 全部随之生效。
llmtest 与 wrap 的末段 ≠ 仓名，路径不变（oagentkit/llm/llmtest、oagentkit/llm/wrap）。

## [v0.8.0] — 2026-10-19

- 杂务(deps)：oekit 依赖升级（obs v0.4.0→v0.4.1、concurrency v0.16.0→v0.17.0）

## [v0.7.2] — 2026-10-19

- 与 v0.7.1 相比无代码差异（版本号/打包变更）

## [v0.7.1] — 2026-10-19

### 修复(deps)：go.sum 里 obs v0.4.0 的 h1 是 tag 重写**之前**算出来的

org 那次全量 tag/历史重写把同名 tag 指到了新提交，于是同名 tag 对应新内容，
而本仓 go.sum 仍记着重写前的 h1。后果是硬失败，不是「下载慢一点」：
    verifying github.com/oagentkit/<MOD>@<VER>: checksum mismatch
    SECURITY ERROR … the bits may have been replaced on the origin server
本仓最后一次 CI 绿在重写之前，所以这条红一直潜伏着，下次触发 CI 才会炸。
修法是**只替换陈旧的那几行**，不整体重算 go.sum。理由有二：
  1. 逐条比对后确认只有 obs v0.4.0 这几个版本在本仓是陈旧的，整体 tidy 会把无关
     条目一起搅动；
  2. 重算时若沿用共享模块缓存，`tidy` 会复用重写前下载的副本，把旧哈希原样写回
     ——**命令成功、依据是错的**（门面仓 agentkit PR #5 第一次就这么被骗过：
     本地全绿、CI 仍红）。所以核对与验证都用空 GOMODCACHE + GOPRIVATE 直连 VCS，
     让每个第一方模块现取现算，再用 CI 报的 downloaded 哈希与本地 go.sum 逐字对账。
三方模块条目一条未动，sumdb 校验面没被关掉。

## [v0.7.0] — 2026-10-18

### 新增：llm 仓建立——模型客户端（3 包）

从 agentkit 单仓切出 tier2 集成层模块，module 路径 github.com/oagentkit/llm。
收编 3 个包（原 llm/ llm/wrap llm/llmtest，按三层路径方案调整）：
- llm       模型客户端：多模型降级链、任务级预算、成本记账、分阶段路由、主备切换
- wrap      调用包装与限流适配
- llmtest   模型层契约测试套件
矩阵内依赖：obs、runtime（breaker 过渡期）、textx。
外部地板 eino + go-openai + eino-ext + x/time。
AGENTS.md 三条纪律：降级链每跳须记录原因、预算判定必须在调用前、RunSafe* 的 panic 收编代价。
门禁：go build / go vet / go test -race 全绿，gofmt 干净。

### 修复(llm)：go.sum 去重（v0.13.1 重复两行）

前两次提交各追加了一行 concurrency v0.13.1，go mod tidy -diff 判漂移。
熔断改动本身（移除本地准入否决兜底、全量改用上游 GenBreaker、
oekit/concurrency 升 v0.13.1）在上一提交已就位，本提交只修 go.sum。
本地门禁全绿：go build / go vet / gofmt / go test -race（3 包）/
golangci-lint 0 issues / go mod tidy -diff exit 0。

### 修复(llm)：go.sum 按 Go 规范顺序重排

上一提交手工插入 v0.13.1 两行时用了字符串序，Go 的 go.sum 用【语义版本序】
（v1.2.2 < v1.10.0），go mod tidy -diff 门禁判漂移。重排为 mod 路径字典序 +
版本 semver 序。
熔断改动（移除本地准入否决兜底、改用上游 v0.13.1）本身 Test job 已全绿。

### 修复(llm)：删除未使用的 defaultBreakerProbeTimeout 常量

BreakerProbeTimeout 的缺省不是固定值，而是按「单模型最坏在途」推导：
(RetriesPerModel+1) × (MaxDelay + 单次尝试预算)。原先额外留了个一分钟兜底常量，
但没有任何调用点——golangci-lint unused 报出。

### 重构(llm)：移除本地熔断准入否决兜底，改用上游 v0.13.1

上游 oekit/concurrency v0.13.0 修好了 #25 —— 计数型触发此前只切状态、切不了放行决策
（放行路径只读速率型 dropRatio、不看 b.state），本仓当时在 llm/breaker.go 里用一个
本地计数器补上准入否决。上游修好后那段是纯冗余，已删除。
现在 breaker.go 只做一件事：按模型名分桶 + 转发到上游 GenBreaker。状态迁移、冷却期、
探测时限、代绑定、计数型触发全部归上游维护。
- 删除 modelBreaker（本地 consecFails / vetoUntil 计数与锁）
- newBreakers 签名从 (trip, cooldown, opts...) 收敛为 (opts...)：
  trip/cooldown 现在直接经由 WithConsecutiveFailures / WithCooldown 传给上游
- allow 不再需要本地 ctx 预检（上游 AllowGen 自带 ctx 门）
- 依赖升级 oekit/concurrency v0.12.0 -> v0.13.1
公开 API 不变：ResilientConfig 的 BreakerTrip / BreakerCooldown / BreakerProbeTimeout
原样保留，各自映射到上游对应选项。
验证：上游修复用独立探针确认 5/5 轮均在第 N+1 次放行返回 ErrCircuitOpen。

### 重构(llm)：熔断实现切到 oekit/concurrency/breaker，摘除对 runtime 的依赖

新增 llm/breaker.go 适配层：
- 状态机（显式三态 / 代绑定 / 冷却期 / 探测时限 / 计数型触发）委托上游 GenBreaker
- 按模型名分桶：降级链要按模型独立统计，一个模型连挂不能误伤另一个
- 补「计数型准入否决」：上游 WithConsecutiveFailures 只切状态、切不了放行决策
  （实测阈值 3、第 12 次才真正拒绝），已报上游 concurrency#25；
  在上游修好前本地用计数器兜底——计数器只做「连挂 N 次→冷却期满前拒绝」，
  状态迁移/窗口统计/探测配对仍归上游。上游修好后删掉这段即可。
- 熔断默认值（3 / 5min / 1min）下沉为 llm 本地常量：它们是 ResilientConfig 的
  API 契约，不随上游变化
公开 API 不变：ResilientConfig 的 BreakerTrip / BreakerCooldown / BreakerProbeTimeout
原样保留，各自映射到上游对应选项 —— 需求 #19/#20 落地后有了等价物，无需废弃。
go.mod 摘除 oekit/concurrency 之外对 runtime 的依赖（本仓不再引用它的任何包）。
门禁：go build / go vet / go test -race 全绿（含 TestResilientBreakerTrips 与
TestResilientUserCancelDoesNotTripBreaker 两条熔断契约测试），gofmt 干净。

- 杂务(deps)：依赖地板升到「历史最新 + 0.1.0」
- 杂务(deps)：跟进最新 tag
- 杂务(deps)：oekit 依赖升级
- 杂务(deps)：跨仓依赖升级
- 杂务(deps)：oekit/concurrency v0.6.0 -> v0.9.0

- CI：接入 org 共享门禁（规则本体在 oagentkit/.github）
