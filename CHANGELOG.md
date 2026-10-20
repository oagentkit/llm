# llm CHANGELOG

> 本文件由 `matrix-tools/scripts/gen_changelogs.py` 从 git 历史生成，按 tag 切段、取每段内的 conventional commit 渲染。

## [v0.14.2] — 2026-10-20

### 修复(pricing)：覆盖键归一化——调用方显式配的单价不再被内建参考价顶掉

承接 oagentkit/llm#26。`NewPriceTable(overrides)` 把 overrides 的键**原样**存进表里，
而 `Lookup` 只对**查询侧**跑 `normalizeCandidates`。净效果：
- 覆盖键**带**日期后缀或部署前缀时，只有**同样形态**的查询能命中它；
- 不带形态的查询会**静默命中内建参考表**——`Found()` 仍为 `true`、命中方式还是
  `exact`，调用方以为「有价」，实际用的是内建快照的价，**调用方显式配的单价被无视**。
影响是**静默**的：不报错、`Found()` 为 true、`kind` 还是 `exact`——没有任何信号提示
「这个价不是你配的」。按内建价记账 = 与调用方意图不符的成本数字。
## 根因
- `port/pricing.go` `newPriceTable`：`t.table[k] = v` 原样赋值；
- `port/pricing.go` `Lookup`：只对**查询侧**归一化；
- 而内建价表 `builtinPricing` 的键是**基础形态**（无日期后缀、无部署前缀）——
  同一个表里两套键形态，覆盖项因此可能「够不着」基础形态的查询。
`WithOverride` 有同样缺陷。`NewPriceTable` 的文档**早已承诺**了正确行为
（「overrides 的键走与内建表同一套归一化规则」）——**文档是对的，代码是错的**，
本修复是让实现对齐既有承诺。
## 改动（`port/pricing.go`）
- 表内**统一按基座形态存键**：新增 `baseForm()`（去部署前缀 + 去日期后缀，
  与 `normalizeCandidates` 的 C2 档一致，即内建表的键形态）；覆盖项入库前先折算。
  ⇒ 覆盖是**双向**的：覆盖 `mimo-v2.5-pro` 能盖住 `mimo-v2.5-pro-20250101`，
  覆盖 `mimo-v2.5-pro-20250101` 同样能盖住 `mimo-v2.5-pro`。
- 新增 `overridePriority()`：同一基座上多个覆盖键的**全序**优先级
  （基础形态 > 更长的键 > 字典序小的键），并显式排序后入库 ⇒ 结果与
  `map` 迭代顺序、与进程都无关（随机迭代顺序会让同一份配置在不同进程选出不同的价）。
- 表值类型 `map[string]Price` → `map[string]priceEntry{price, key, override}`：
  保留每一项价的**原始键**（便于审计「这个价是谁配的」），并让
  `PriceLookup.Key` 报原始键——`Lookup(Key)` 必然拿到同一份价。
- `Kind` 判据改为「原始键 == 查询名 ⇒ `MatchExact`」：覆盖键原样查回仍报 exact，
  走归一化才对上则报 normalized。
- `WithOverride` 改走同一套 `applyOverride`，与 `NewPriceTable` 规则一致。
**公共 API 签名零改动**（`PriceLookup` 未加字段——加字段会破坏外部未命名字段
复合字面量的源码兼容）。
## 遮蔽风险（已实证排除）
归一化只去「最后一个 `/` 之后」和「结尾日期」两段：`gpt-4o-mini-2024-08-06` 折出来
是 `gpt-4o-mini` 而**不是** `gpt-4o` ⇒ 同主干异价 SKU 不会被跨族吞掉（保留
`Lookup` 刻意不做前缀回落的设计）。另用 `TestBuiltinPricingKeysAreBaseForms`
把「内建表 19 个键每一个本身就是基座形态」钉住——将来加价目表键时若违反会立刻红。
并对内建表 18 个模型 × 18 个变体做了交叉检查：**0 处遮蔽、0 处漏查**。
## 验证
新增 13 个测试（`port/pricing_test.go`）+ 根包 issue 复现测试
（`pricing_test.go`），覆盖：覆盖键带日期后缀 / 带部署前缀 / 基础形态盖住变体、
内建表不受影响、优先级规则与严格全序、同族只留一个价、`WithOverride` 同规则、
不遮蔽兄弟模型、内建键形态、查回键的幂等性、并发安全。
另做了**反向验证**：临时把覆盖键改回「原样入库」，两个用例立刻失败并复现 issue
的原始症状（`Lookup("mimo-v2.5-pro")` got={1 3} want={9 9}、
`Lookup("gpt-4o")` got={2.5 10} want={1 2}），随后已还原——证明测试确实能抓住这个 bug。
```
$ go build ./... && go vet ./... && gofmt -l .
$ go test -count=1 ./...         ok（5 个包）
$ go test -count=1 -race ./...   ok（5 个包）
```
关联：oagentkit/llm#26

- 文档(changelog)：按新生成器规则重生成 CHANGELOG.md

生成器修复（oagentkit/matrix-tools#43）落地：
① `[round-N] ` 前缀被剥离 → conventional commit 解析不再失效，
   **commit body 保留**（此前整段被丢弃，发布说明只剩标题）；
② 非 conventional 提交也保留 body；
③ `--check` 识别「待发版段」（预写了但还没打 tag 的那段）。
本 PR 恢复的是**本就在 commit body 里**的正文（迁移步骤、代码块、
判据锚点）——此前被 `[round-N] ` 前缀导致的解析失败丢弃了。
**没有任何发布说明内容被删除**：删除的行都能在新增行里找到对应
（规范化掉 `(#N)` 与 `[round-N] ` 前缀后逐行比对）。
关联：oagentkit/matrix-tools#43、oagentkit/matrix-tools#40


## [v0.14.1] — 2026-10-20

- 与 v0.14.0 相比无代码差异（版本号/打包变更）

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

### 修复(go)：Go 地板降到 go 1.24.0，CI go-version 同步到 1.24

承接 matrix-tools#22（tier0 三仓的地板被无谓抬高）——本轮把 tier1+ 里
**依赖链全兼容**的仓也降下来（matrix-tools#34 的排查结论）。
## 依据
- **依赖链上没有要求更高版本的包**：本仓的每个依赖（直接与间接）的 go 指令
  都 ≤ 1.24.x，本仓声明 1.26.0 没有任何依赖需要它。
- **代码本身兼容 1.24**：真实 go1.24.0 工具链（`GOTOOLCHAIN=go1.24.0`）
  build + test 全绿——实跑，不是推断。
- **门槛是 go 指令不是 toolchain**（编译器实证）：`go 1.28.0` 被拒，
  `go 1.24.0` + `toolchain go1.28.0` 通过。
## toolchain 行的取舍：删掉
toolchain **不构成门槛**，但 `toolchain go1.27.2` 会让 `go` 命令**自动下载**
1.27.2，与「最低支持版本是 1.24」相悖。删掉，也与基座 oekit 六个基础仓一致。
## 依赖地板同步重钉
降 go 指令后，`require` 必须指向**已发新 tag 的 tier0 仓**（它们的 tag 快照
里才是 go 1.24.0）——否则 MVS 会拉到旧 tag 的 1.26 地板，降级形同虚设。
本仓的内部 require 已随 `go mod tidy` 一并抬到对应的新 tag。
## 改动
- `go.mod`：`go 1.26.0` → `go 1.24.0`；删 `toolchain go1.27.2`；
  内部 require 抬到 tier0 新 tag
- `.github/workflows/ci.yml`：`go-version: '1.26'` → `'1.24'`
CI 的 go-version 与 go.mod 的 go 指令**成对**——这是跨层通用纪律，不因本次
降级而放松。CI 用高版本构建、go.mod 承诺低版本时，CI 全绿但用户按最低版本
编译失败，门禁绿了而承诺没兑现。
## 验证
- `GOTOOLCHAIN=go1.24.0 go build ./...` → exit 0
- `GOTOOLCHAIN=go1.24.0 go test ./...` → 全绿
- `go mod tidy` → 零差异
门禁见 oagentkit/matrix-tools：`scripts/verify_go_floor.py`（判据 4 覆盖
tier1+ 相对地板，判据独立不复用任何生成器）。

### 修复(deps)：依赖地板抬到发版前已发布的最新（textx v0.6.0 / oekit/pkg v0.17.0），并修 README 与 go.mod 矛盾

## 依赖地板（llm#10）
v0.12.0（2026-10-20 00:08:18）打 tag 时，`textx v0.6.0`（22:51:42）与
`oekit/pkg v0.16.0`（22:52:53）**已经发布 75 分钟了**，但 go.mod 仍钉着
textx v0.5.0 / oekit/pkg v0.15.1。v0.12.1（01:21:00）原样复制了同一组旧地板
——同款动作连续两次，本仓 CHANGELOG 里至少三次「依赖地板对齐远端最新」的纪律
被打破。
本次按**当前远端最新 tag** 抬（版本号从 git tag 推导，不手写）：
| 依赖 | 改前 | 改后 | 远端最新 |
|---|---|---|---|
| github.com/oagentkit/textx | v0.5.0 | v0.6.0 | v0.6.0 |
| github.com/oekit/pkg | v0.15.1 | v0.17.0 | v0.17.0 |
`github.com/oekit/concurrency` v0.17.1、`github.com/oekit/logx` v0.7.1 本来就是最新，未动。
## README 与 go.mod 的矛盾
README:20 写「本仓依赖的矩阵内仓：`obs`、`textx`」，但 v0.12.0 的 require 段
**只有 textx、没有 obs**——`obsx` 已于 v0.12.0 从 `obs/obsx` 迁入本仓
（README:11 自己写了这件事）。两处说法互相矛盾。
改成与 go.mod require 段逐项一致：只留 `textx`，并注明 obsx 迁入的背景。
## 验证
```
$ GOWORK=off go mod tidy        # exit 0（空 module cache + GOPRIVATE 直连 VCS 重算）
$ GOWORK=off go build ./...      # exit 0
$ GOWORK=off go test ./...       # ok llm / ok llm/llmtest / ok llm/obsx / ok llm/wrap
$ GOWORK=off go mod tidy -diff   # exit 0（无漂移）
```
发 tag 前的地板时效校验（新增于 matrix-tools 的 verify_tag_floor.py）：
```
$ python3 scripts/verify_tag_floor.py --root … --repo llm --ref HEAD
✅ llm HEAD：依赖地板 == 发版时刻的最新，无落后
```
## 遗留
- 本 PR **不包含发 tag**：llm 发新 tag 会让门面 agentkit 的
  `verify_facade_release.py --org oagentkit` 变红（门面最新 tag 仍钉 llm v0.12.1），
  那是跨仓的顺序窗口，会拦住不相关的 PR。正确的顺序是：
  ① llm 发 v0.12.2 → ② 门面 go.mod 抬 llm v0.12.2 → ③ 门面发 tag → ④ 刷新
  matrix-tools/versions.json 的 latest + 站点副本。这四步要一起做。

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

matrix-tools 的 verify_dep_floors.py 首次全矩阵扫描查出的地板漂移，本仓是受影响仓之一。
本轮按 tier 拓扑顺序推进：tier0、tier1 先发，本仓再把地板跟到它们的新 tag——
否则就是「发了新的、旧的立刻变落后」，永远收敛不了。
所升版本与原钉版本相比源码零改动，逐版实测确认：
- textx v0.4.0→v0.5.0 / obs v0.5.0→v0.6.0 / guardx v0.4.1→v0.5.0 /
  runtime v0.5.0→v0.6.0 / mcpkit v0.6.0→v0.7.0  全树逐字节相同（diff -rq 退出码 0）
- oekit/pkg v0.14.0→v0.15.1  仅新增 urlguard 包（纯标准库，零新增三方依赖）
- oekit/concurrency v0.17.0→v0.17.1  仅三个文档文件
- oekit/logx v0.7.0→v0.7.1  仅 CI 配置与文档
无 API 变更、无兼容风险。
本地门禁：go mod tidy -diff 无输出 / go build / go vet / gofmt / go test -race 全通过。
关联 oagentkit/matrix-tools#13


## [v0.10.0] — 2026-10-19

- 杂务(deps)：依赖地板对齐远端最新（obs v0.5.0 / textx v0.4.0 / concurrency v0.17.1 / pkg v0.15.1 / logx v0.7.1）

matrix-tools 的 verify_dep_floors.py 首次全矩阵扫描查出：43 条第一方依赖里
28 条落后，横跨 11 个仓；本仓是其中之一。
所升版本与本仓原钉版本相比源码零改动，逐版实测确认：
- textx v0.3.0→v0.4.0 / obs v0.4.1→v0.5.0 / runtime v0.5.0→v0.6.0 /
  mcpkit v0.5.1→v0.6.0  全树逐字节相同（diff -rq 退出码 0）
- oekit/pkg v0.14.0→v0.15.1  仅新增 urlguard 包（纯标准库，pkg 的 go.mod
  连 require 块都没有，零新增三方依赖）
- oekit/concurrency v0.17.0→v0.17.1  仅三个文档文件
- oekit/logx v0.7.0→v0.7.1  仅 CI 配置与文档
无 API 变更、无兼容风险。
本地门禁：go mod tidy -diff 无输出 / go build / go vet / gofmt / go test -race 全通过。


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

本 PR 含两项改动。
## 1. oekit 依赖地板
姊妹矩阵 oekit 的 `concurrency` 出了 v0.17.0，依赖地板一并对齐。
升级前已核对 `oekit/concurrency` v0.16.0...v0.17.0 的全部 2 个提交：
- `feat(ratelimit): KeyedLimiter.Create 护栏内原子登记 (Fixes #33)` — `ratelimit/keyed.go` +28 行
- `docs: 矩阵总览表 DESC 口径同步` — 仅 AGENTS.md / README.md / CHANGELOG.md
无 breaking / remove 标记，未触及任何已有导出签名，故为纯补丁式升级。
## 2. 迁出被 force 移动过的 tag
本仓原先依赖的姊妹仓 tag（发布后被强推到了重写后的 main）内容已与
sum.golang.org 记录不一致，于是任何走 `proxy.golang.org` 或 `GOPROXY=direct`
的构建都会 SECURITY ERROR。本地往往不报错——模块缓存预热 + goproxy.cn
还留着移动前的字节，把问题掩盖了。
已为 8 个模块重打不可变的**附注 tag**（内容与被移动 tag 指向同一 commit，
只换版本号，让校验和数据库重新记录一次正确哈希），并把依赖迁过去。
`go.sum` 的 h1 全部取自 sum.golang.org，仅就地替换目标模块的两行
（zip h1 + go.mod h1），其余行不动。


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

本轮做了版本线重置：清空全部历史 tag，按「历史最新 + 0.1.0」重打新版本
（如 llm v0.6.0 → v0.7.0、toolgate v0.6.1 → v0.7.0、agentkit v1.3.0 → v1.4.0）。
各仓 go.mod 的矩阵内依赖地板同步跟进到新版本号。
**go.sum 的 oagentkit 行已整段删除**，由 CI 的 `go mod tidy` 重算：
新 tag 的内容与任何历史版本都不同，`h1` 必须现算；本地没有 Go 工具链，
且 `/go.mod` 的 dirhash 无法自行复现（zip hash 可复现，gomod hash 不可复现）。
重算依赖 `.github` 的 go-ci 配了 `GOPRIVATE=github.com/oagentkit/*` 直连 VCS：
goproxy.cn 缓存着旧版本，若走 proxy 会拉回**旧代码**并算出旧 hash，
门禁全绿但依赖是错的。
（第二个提交会补上重算好的 go.sum。）
* chore(deps): 依赖地板升到「历史最新 + 0.1.0」
chore(deps): 依赖地板升到「历史最新 + 0.1.0」
本轮做了版本线重置：清空全部历史 tag，按「历史最新 + 0.1.0」重打新版本
（如 llm v0.6.0 → v0.7.0、toolgate v0.6.1 → v0.7.0、agentkit v1.3.0 → v1.4.0）。
各仓 go.mod 的矩阵内依赖地板同步跟进到新版本号。
**go.sum 的 oagentkit 行已整段删除**，由 CI 的 `go mod tidy` 重算：
新 tag 的内容与任何历史版本都不同，`h1` 必须现算；本地没有 Go 工具链，
且 `/go.mod` 的 dirhash 无法自行复现（zip hash 可复现，gomod hash 不可复现）。
重算依赖 `.github` 的 go-ci 配了 `GOPRIVATE=github.com/oagentkit/*` 直连 VCS：
goproxy.cn 缓存着旧版本，若走 proxy 会拉回**旧代码**并算出旧 hash，
门禁全绿但依赖是错的。
（第二个提交会补上重算好的 go.sum。）
* chore(deps): 回填 CI 重算的 go.sum
`h1` 由 CI 里真实的 `go mod tidy` 算出后回填——本地没有 Go 工具链，
且 `/go.mod` 的 dirhash 无法自行复现（zip hash 可复现，gomod hash 不可复现），
所以这是唯一权威来源。
重算依赖 `.github` go-ci 里的 `GOPRIVATE=github.com/oagentkit/*` 直连 VCS：
goproxy.cn 仍缓存着版本线重置前的旧内容，走 proxy 会拉回**旧代码**并算出旧 hash，
门禁全绿但依赖是错的。
验证方式就是本 PR 的 `go mod tidy -diff`：若回填有任何偏差，它会照样报差异。
* chore(deps): 回填 CI 重算的 go.sum
`h1` 由 CI 里真实的 `go mod tidy` 算出后回填——本地没有 Go 工具链，
且 `/go.mod` 的 dirhash 无法自行复现（zip hash 可复现，gomod hash 不可复现），
所以这是唯一权威来源。
重算依赖 `.github` go-ci 里的 `GOPRIVATE=github.com/oagentkit/*` 直连 VCS：
goproxy.cn 仍缓存着版本线重置前的旧内容，走 proxy 会拉回**旧代码**并算出旧 hash，
门禁全绿但依赖是错的。
验证方式就是本 PR 的 `go mod tidy -diff`：若回填有任何偏差，它会照样报差异。
* chore(deps): 回填 CI 重算的 go.sum
`h1` 由 CI 里真实的 `go mod tidy` 算出后回填——本地没有 Go 工具链，
且 `/go.mod` 的 dirhash 无法自行复现（zip hash 可复现，gomod hash 不可复现），
所以这是唯一权威来源。
重算依赖 `.github` go-ci 里的 `GOPRIVATE=github.com/oagentkit/*` 直连 VCS：
goproxy.cn 仍缓存着版本线重置前的旧内容，走 proxy 会拉回**旧代码**并算出旧 hash，
门禁全绿但依赖是错的。
验证方式就是本 PR 的 `go mod tidy -diff`：若回填有任何偏差，它会照样报差异。

- 杂务(deps)：跟进最新 tag

oagentkit/obs v0.2.1→v0.3.0、oagentkit/textx v0.1.0→v0.2.0
go.sum 取 sumdb 权威 h1，仅就地替换目标 mod 两行。

- 杂务(deps)：oekit 依赖升级

concurrency v0.13.1→v0.16.0、pkg v0.13.0→v0.14.0
姊妹矩阵 oekit 的依赖地板此前被漏掉（上一轮只扫了 oagentkit 内部依赖），本次一并对齐到最新。
升级前已确认 oekit/concurrency v0.13.1..v0.16.0 的 8 个提交全是 feat/docs/chore，
无 breaking/remove 标记。其中 #28 订正了 `WithCooldown(0)` 的语义——
即 llms.txt 里警告过的那个陷阱，升上去正好拿到修正后的行为。
go.sum 的 h1 取自 sumdb，仅就地替换目标 mod 的两行、其余行不动。

- 杂务(deps)：跨仓依赖升级

obs v0.1.0 → v0.2.1
全 org 跨仓依赖地板统一升级。go.sum 的 h1 取自 sumdb（经 goproxy.cn 代理），
即 `go mod tidy` 会写入的同一个权威值；只就地替换该 mod 的两行、其余行一个字节不动，
不重排全文件（go.sum 行序由 cmd/go 自己维护，手写重排会引入漂移）。

- 杂务(deps)：oekit/concurrency v0.6.0 -> v0.9.0

拿到上游显式三态状态机（#17）与 singleflight 可观测折叠（#18），并为本次
breaker 替换评估了语义差异。
用到的子包只有 async / singleflight / syncx —— 上游 breaker 包我们并未引用
（runtime/breaker 是本仓自己的三态实现），所以本次升级零 breaking。
关于 breaker 替换的现状：#17 已落地（closed/open/half-open + 代绑定 + 探测时限 +
开放期失败不延长冷却），但两侧的触发语义仍不对齐，已在上游补两个需求：
- #19 冷却期与探测失联时限共用 probeTimeout，无法独立配置
- #20 缺「连续失败 N 次即熔断」（trip）触发
因此本包暂不删除 —— llm.TestResilientBreakerTrips 钉的是 trip 语义，
上游 v0.9.0 只有速率型触发（dropRatio > 0），现在换会让该测试失败。


- CI：接入 org 共享门禁（规则本体在 oagentkit/.github）
