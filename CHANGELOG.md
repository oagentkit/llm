# llm CHANGELOG

> 本文件由 `matrix-tools/scripts/gen_changelogs.py` 从 git 历史生成，按 tag 切段、取每段内的 conventional commit 渲染。

## [v0.21.12] — 2026-11-13

### 修复：sweep2 批量修复 5 单——obsx finish_reason/PreviewLen 契约、port 熔断缺省、llmtest ResponseMeta 与字段契约（#164-#168）

- #164 obsx 非流式 onEnd 补落 finish_reason（复用单源 finishReasonOf，与流式路径对齐）
- #165 port.NewBreakers 铺底 DefaultBreakerTrip/Cooldown（缺省 3 连败 / 5min 冷却），
  调用方 opts 后写覆盖——「未传」与「显式传」可区分，port.Chain 端到端钉住
- #166 llmtest Stream 按 Resp 填 ResponseMeta（respMetaOf 单源，Generate/Stream 共用）
- #167 llmtest 四处字段契约：Completion-only 也落 usage、ToolTag 派生副本真追加、
  StreamContent 文档改回落口径、耗尽编号随真实调用序（Calls 同源自增）
- #168 obsx Options.PreviewLen 兑现：输入侧 input_preview、流式侧 output_preview
  （drainStream 累计 delta）、input_len/output_len 恒落（0 = 只落长度）

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.34.1

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.34.0` | `v0.34.1` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务：go 1.25+ 语言特性纯现代化——range-over-int / builtin min-max / wg.Go / slices.Sort / slices.Sorted(maps.Keys)（行为零变化）

- rangeint：计数循环转 for range N（client.generateJSONFitted 回喂轮、port.Chain.delay、resilient/wrap/pricing/llmtest/ratelimit 等测试循环）
- minmax：手写 if/else 上下限收编 builtin min/max（backoffDelayJittered、BudgetConfig、openai.fillDefaults、BusyAdmissionDelay、fit L3 keep）
- waitgroupgo：recovery_test 干净 Add(1)/go/Done fan-out 收编 wg.Go（async.GoSafe 封装的 pricing 并发回归不动）
- slices.Sort：compat_test sort.Strings ×3 → slices.Sort
- slices.Sorted(maps.Keys)：decision.firstCandidate map 键收集+排序
- errors.go isDigitToken 字符串字节扫描转 for i := range len(s)（保持字节语义）
- 不动项（保守跳过）：errors.AsType / new(expr) / maps.Copy / slices.Backward / strings.Cut 等规则外建议；errors.go containsMarker 子串扫描（非计数循环）；pricing.go 自定义 SortFunc 键排序；fit.go/assemble.go 无后置语句的 while 形循环


## [v0.21.11] — 2026-11-12

- 杂务(deps)：依赖地板追到最新——github.com/oekit/pkg v0.46.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/pkg` | `v0.45.0` | `v0.46.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——oekit 基座 wave（concurrency v0.34.0 等）

chore(deps): 依赖地板追到最新——github.com/oekit/concurrency v0.34.0
| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.33.0` | `v0.34.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.11.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.10.9` | `v0.11.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(modernize)：sort.* → slices.*/min-max 内建——吃 Go 1.21+ 标准库，零行为变化

- 测试(compat)：ParseDir → os.ReadDir+ParseFile 定向替身——go 1.26 下 staticcheck SA1019（ParseDir 自 1.25 弃用）

兼容面扫描只需要「目录下直接包含的非测试 .go 文件」平铺集，不需要
build-tag 包关联语义；混包检测改为逐文件核对包名（ParseDir 分组语义的
等价守卫）。stdlib-only 不引 x/tools。


## [v0.21.10] — 2026-11-11

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.10.9

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.10.8` | `v0.10.9` |
| `github.com/oekit/pkg` | `v0.43.0` | `v0.44.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。


## [v0.21.9] — 2026-11-11

### 修复(client,resilient,openai,port,obsx,provider)：七单清零——免费双口径、估算漏计、nil 链元素、熔断记账口径、boost 粘性、链层超时、ErrKey（#138-#144）

* fix(client,resilient,openai,port,obsx,provider): 七单清零——免费双口径、估算漏计、nil 链元素、熔断记账口径、boost 粘性、链层超时、ErrKey（#138 #139 #140 #141 #142 #143 #144）
- #138 provider.CostPer1KTokens 契约自破收口：文档删掉「0 表示不计费」，
  「免费」唯一合法表达 = PricedProvider 返回 (Price{}, true)（零价也是
  显式定价，不回落价表），测试钉死三路优先级与端到端免费记账
- #139 无 usage 估算漏计：estimateMsgTokens 计入 ToolCalls（工具名+参数
  JSON）与三组多模态文本部件，口径对齐 textx/compact 的 msgTokens——
  大入参工具调用不再系统性低估、预算短路恢复有效
- #140 NewResilient 构造期逐元素 nil/typed-nil fail-fast（queue#71 同款
  纪律），错误文案带链上序号——不再把 panic 留给调用方 goroutine
- #141 port.Chain.attempt 熔断记账对齐 Resilient：一次 Allow 放行（含 N
  次物理重试）只在收口报一次 Failure——WithConsecutiveFailures(3) 不再
  被单个逻辑请求整体烧穿
- #142 截断提升粘性：boost 记在「当前生效参数」上，提升轮撞瞬态失败后续
  命重试轮保持提升上限——不再静默退回原上限再截断、被上层误判「提升后
  仍截断」而放弃；提升轮真的再截断才止损
- #143 注入 HTTPClient 后 AttemptTimeout() 恒 0：超时归注入方 own，
  Resilient 链不再叠加 per-attempt 截止——「本仓不再叠加自己的 Timeout」
  对 HTTP 层与链层同时成立
- #144 llm.call.error 错误字段走 logx.ErrKey（"err"），不再手写 "error"
  字面量——两矩阵落同一 Loki/ES，查询侧不用 OR 双写
range-over-int（Go 1.22）用于两处重试循环；测试 8 条回归用例按 issue 号
命名，回归时按号检索。
* style: gofmt（issue_sweep_test.go 结构体字面量对齐、openai.go 注释对齐）

- 杂务(deps)：依赖地板追到最新——guardx v0.24.2 / textx v0.10.8 / runtime v0.12.7（2026-11-11 列车）
- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.33.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.32.0` | `v0.33.0` |
| `github.com/oekit/pkg` | `v0.42.0` | `v0.43.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.32.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.31.2` | `v0.32.0` |
| `github.com/oekit/logx` | `v0.9.1` | `v0.9.2` |
| `github.com/oekit/pkg` | `v0.41.1` | `v0.42.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.10.7

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.10.6` | `v0.10.7` |
| `github.com/oekit/concurrency` | `v0.31.1` | `v0.31.2` |
| `github.com/oekit/logx` | `v0.9.0` | `v0.9.1` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。


## [v0.21.8] — 2026-11-10

### 修复(recovery,resilient,client)：busy 退避表空表 panic、WithBudget 漏抄价表、OnUsageDetailed 防重缺口（#130 #131 #132）

fix(test): recovery_test 弃 append 返回值赋回——ineffassign（lint 门禁红）

- 杂务(deps)：依赖地板追到最新——github.com/oekit/pkg v0.41.1

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/pkg` | `v0.41.0` | `v0.41.1` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.31.1

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.31.0` | `v0.31.1` |
| `github.com/oekit/logx` | `v0.8.12` | `v0.9.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。


## [v0.21.7] — 2026-11-09

### 修复(errors)：containsMarker 前侧小数排除——"12.429" 分数尾不再假阳性限流/确定性失败（#125）

后侧排除（#114）的镜像侧：marker 前是「数字+小数点」时 marker 是小数
的分数尾，不是独立状态码。isDecimalTail 与 isDecimalContinuation 对称。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/pkg v0.41.0

## [v0.21.6] — 2026-11-09

### 修复(wrap)：ContinueModel 续写轮返回 ToolCalls 以续写轮为基座合并，工具调用决策不再静默丢弃（#116）

- a3e1465

### 修复：llm 第三轮 issue 清零（#114 #115）

* fix(errors): 数字 marker 词边界排除「小数点+数字」——"429.5ms" 不再假阳性限流白等退避（#114）
* fix(usage): OnStart 起点改为最近一次调用起点——嵌套作用域 DurationMS 不再从根起点单调虚涨（#115）

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.10.6

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.10.5` | `v0.10.6` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/pkg v0.40.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/pkg` | `v0.39.0` | `v0.40.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.10.5

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.10.4` | `v0.10.5` |
| `github.com/oekit/concurrency` | `v0.30.1` | `v0.31.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.10.4

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.10.3` | `v0.10.4` |
| `github.com/oekit/concurrency` | `v0.29.0` | `v0.30.1` |
| `github.com/oekit/logx` | `v0.8.11` | `v0.8.12` |
| `github.com/oekit/pkg` | `v0.38.0` | `v0.39.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。


## [v0.21.5] — 2026-11-08

- 与 v0.21.4 相比无代码差异（版本号/打包变更）

## [v0.21.4] — 2026-11-08

### 新增：WithRateLimitRetry 模型级 429 退避装饰——RawModel 直驱路径弹性补位（#100）

弹性栈（Resilient 降级链/熔断/预算 + Client.generateRetry 同模型重试）只
覆盖 Generator 自家路径；RawModel() 返回的裸 eino 模型被 ReAct 直接驱动时
429 一次失败即硬错——bianque 为此手搓完整 RetryModel 装饰器（552 次 429
灭三专家的生产实证）。
新增 WithRateLimitRetry(inner, opts...)（根包，与 breaker/限速装饰器同族）：
- 只重试限流类失败（ClassifyLLMError→IsRateLimit），非限流错误原样快速
  上抛；ctx 取消立即让位
- 缺省曲线 = bianque 生产实证形态：5 次尝试、2s 基准、32s 封顶、0.25 单边
  向上抖动；WithRetryAttempts/BaseDelay/MaxDelay/JitterRatio 可配
- 退避曲线与 Retry-After 取舍与 Client.generateRetry 单源同化：
  backoffDelayJittered/waitBackoffGap（抖动器可配，包级 retryJitter 行为不变）
- ToolCallingChatModel 双形态：WithTools 派生沿用同一退避配置；
  IsCallbacksEnabled 转发内层（llm#86 防 usage 双计）
- 流一旦建立即原样交还（本层不触碰流生命周期）；装饰位指引：包在限速治理
  层之外（重试重入重新排队，退避不占在飞配额）
- 兼容层 llm/llm 镜像新符号（TestCompatParity 红转绿）
兼容性取舍：纯 opt-in 装饰器，RawModel()/RawModelWithFailover() 默认行为
不变（直驱用户的时延语义不受影响）；默认开启留待观察期后另议。
close #100

- 杂务(deps)：依赖地板追到最新——基座 v0.38.0/v0.29.0/v0.8.11 + 矩阵 tier0 新 tag（按本仓依赖取齐）

发版时刻地板时效门禁要求追平。
兼容性实测：go build / go vet / go test -race -count=1 ./... 全绿，go mod tidy -diff 无漂移。


## [v0.21.3] — 2026-11-08

- 杂务(deps)：依赖地板追平 oekit BOM v0.2.9——pkg v0.37.4 / logx v0.8.10 / concurrency v0.28.4

## [v0.21.2] — 2026-11-08

- 杂务(deps)：依赖地板追到最新——concurrency v0.28.4 / logx v0.8.9（按本仓依赖取齐）

oekit 基座 v0.28.4 / v0.8.9 落地，发版时刻地板时效门禁要求追平（matrix-tools#188 发版链）。
兼容性实测：go build / go vet / go test -race -count=1 ./... 全绿，go mod tidy -diff 无漂移。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/pkg v0.37.4

pkg v0.37.4（httpjson NormalizeBaseURL scheme 大小写归一 #239 + refresh_versions）落地，地板同步追平（matrix-tools#188 发版链）。
兼容性实测：go build / go vet / go test -race -count=1 ./... 全绿，go mod tidy -diff 无漂移。

- 杂务(deps)：依赖地板追到最新——concurrency v0.28.3 / logx v0.8.8 / pkg v0.37.3 / textx v0.10.1 / guardx v0.23.1 / runtime v0.12.1（按本仓依赖取齐）

发版时刻地板时效门禁（verify_tag_floor --ref HEAD）要求追平（matrix-tools#188 发版链）。
兼容性实测：go build / go vet / go test -race -count=1 ./... 全绿，go mod tidy -diff 无漂移。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.28.1

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.28.0` | `v0.28.1` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。


## [v0.21.1] — 2026-11-07

### 修复(stagerouter)：UsedTokens 去重键改指针身份——不可比 TokenAccountant 实现不再 panic（#81）

根因：UsedTokens 以接口值做去重 map 键（seen := map[TokenAccountant]struct{}）。
Go 规定接口键的动态类型不可比（含 slice/map 字段的值类型，如带 []string
label 的 Prometheus 记账器）时 map 读写直接运行期 panic——文档又明确邀请
自定义 TokenAccountant 实现，此类实现经 BudgetHolder.BoundBudget() 进路由
即炸。
修法：去重键改由 accountantKey 生成「类型 + 身份」字符串，按动态类型分支：
- 指针类（指针/chan/map/func/slice，覆盖 *Budget 等指针接收者实现）：
  %T@%p 即指针身份，同一实例多次传入只计一次；
- 其余（值类型实现）：%T@%#v 内容身份——reflect.Value.Pointer 对 struct
  等非指针类同样 panic（本机已验证），值类型没有指针身份，等值即视为
  同一记账器。
同步更新 BudgetHolder/UsedTokens 注释口径。共享 *Budget 的指针身份去重
语义不变（原 TestStageRouterUsedTokensSharedBudgetDedup 继续通过）。
测试：新增 TestStageRouterUsedTokensIncomparableAccountant——含 []string
字段的不可比值类型 TokenAccountant 经 BoundBudget 注入 4 条链：已验证
修前该用例在 seen[acc] 处 panic，修后不 panic、等值去重只计一次
（40 而非 120）、不同值各计一次（55）。

- 杂务(build)：补统一 Makefile——本地 ci 门禁与共享 CI 同口径（agentkit#138）
- 杂务(deps)：依赖地板追到最新——github.com/oekit/logx v0.8.6

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/logx` | `v0.8.5` | `v0.8.6` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/pkg v0.37.1

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/pkg` | `v0.37.0` | `v0.37.1` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。


## [v0.21.0] — 2026-11-07

### 修复：流式观测/计量/端口面四连修（#80 #86 #87 #88）

#80 流式 usage 观测 EOF 后不 Close 流：
根因：wrap/fit.go observeUsage 只在错误路径手动 sr.Close()、正常 EOF 直接
return，违反 eino「reader 必须恰好 Close 一次（EOF 后也必须 Close）」契约；
内层流经 Copy 扇出（eino callbacks/ADK 分发形态）时子流不 Close 则父流
closedNum 永不收口，副本链分片滞留。修法：EOF 分支补 sr.Close() 且先关
内层再关外层（w.Close 的 EOF 可见性建立 happens-before，消费方可无竞态
断言契约）；Close 不幂等（裸 Pipe 流 double-close 会 panic），故每条退出
路径显式恰好关一次、不用 defer 兜底。修此问题时另暴露并修复同函数一处
判反潜伏 bug：w.Send 返回值是 closed（true=消费端已关），原 !w.Send 在
投递成功时退出且不关外层——只透传首块、外层永无 EOF，消费方第二帧起
挂死；因无测试消费过 FitModel 流输出而潜伏至今。补 Copy(2) 扇出契约测试。
（issue 中同类 agent/agentrun.go 一处在 agent 仓，需另行修复。）
#86 usage 装饰器未实现 eino Checker：
根因：FailoverModel/FitModel/ContinueModel/RateModel 均未实现
components.Checker（IsCallbacksEnabled），compose 认为组件「不会自己发
回调」，在节点外再注入一轮 OnStart/OnEnd——内层模型自燃回调 + 节点级
注入双发，一次调用产出两条 usage 记录，预算/成本双计（RawModelWithFailover
→ ReAct/compose 图内必现）。修法：单内层装饰器转发
components.IsCallbacksEnabled(inner)；FailoverModel 链上任一链节启用即
true（对齐 eino ADK failover proxy 模式）。补 compose 图真跑回归测试
（未修复时实测 2 条记录：StubOpenAI 自燃 + FailoverModel 节点注入）。
#87 DrainStreamUsage 只从 *schema.Message 帧提取 FinishReason：
根因：eino-ext 流式真实帧是 *model.CallbackOutput（acl/openai 每 chunk
sw.Send(&model.CallbackOutput{Message, Config, TokenUsage})），经
OnEndWithStreamOutput 直达 DrainStreamUsage；#70 修复只在 Message 分支
提取 finish，CallbackOutput 分支漏掉——最主流帧形态上 finish_reason 恒
空，length 截断在流式观测面不可见。修法：CallbackOutput 分支补
t.Message.ResponseMeta.FinishReason 提取，两分支经 finishReasonOf 单
源化；补 CallbackOutput 帧形态断言（含 Message 帧对照组）。
#88 port.Chain WithBreaker(nil) 静默清掉缺省熔断器：
根因：WithBreaker 无 nil 防御，直接 c.breakers = b 覆盖 NewChain 装的
缺省 NewBreakers，首次 Complete 在 c.breakers.Allow 空指针 panic；同构造
器兄弟选项 WithClassifier 显式判 nil、WithBudget/WithTracker 使用点判
nil，唯独 WithBreaker 两头不防。修法：nil = 未设置（保留缺省），与
WithClassifier 同纪律（Breakers 无禁用态，本选项不存在「显式禁用」语
义）；补 nil/非 nil 两路测试（非 nil 路用 WithConsecutiveFailures(1) 钉
自定义熔断器真实接管）。

### 修复(llm)：核心模型面五修——错误分类词边界/failover 尾链 nil/单次尝试限时/截断提升重裁/续写 usage 合并（#77-#85）

## 摘要
核心模型面五修 + 两项已修复核实补回归：错误分类数字状态码词边界（#78）、failover 尾链 nil 掩盖前链真实错误（#85）、Client 单次尝试独立限时（#84）、截断提升轮输入预算重算（#83）、ContinueModel 续写 meta usage 累加（#82）；#77/#79 核实为 #69/#63 已修复，本 PR 补回归测试锁死契约。
close #77
close #78
close #79
close #82
close #83
close #84
close #85
## #78 错误分类数字状态码词边界只防相邻数字
- **根因**：`containsMarker` 对纯数字 marker 只做「前后字节非数字」匹配，`"400ms"`/`"429ms"` 的后边界是字母 `m` 照样命中——`"upstream timeout after 400ms"` 被判 400 确定性失败（单客户端直接放弃重试）、`"failed after 429ms"` 被判 429 限流（白等 5s 退避下限、误采信 Retry-After）。
- **修法**：数字 marker 词边界收紧为「前后非字母数字」（新增 `isAlnumByte`；汉字等多字节首字节仍算边界，`错误429限流` 照常命中）。真状态码形态（空格/标点/括号边界）不受影响，与 #64 起 marker 收窄同方向。
- **测试**：`TestClassifyLLMError` 补 `400ms`/`401ms`/`429ms`/`a429b` 误判用例与 `status 429,`/`(400)` 真形态用例。
## #85 failover 链尾 nil 模型掩盖前链真实错误
- **根因**：链尾 nil（typed-nil 装箱）时 Generate 经 `generateModel` 返回「链含 nil 模型」后因 `i==len-1` 直接上抛，倒数第二节的 429/超时被装配错误覆盖；Stream 同场景 `continue` 落空返回「failover 链为空」——同一故障两个文案且都不是真实根因。
- **修法**：失败分账累计——真实模型错误以最新为准（沿旧语义），nil 链节装配错误（`errNilLink` 单源文案）只记录不覆盖；`chainExhaustedErr` 收口时两者 `errors.Join` 并列（真实错误在前），只有 nil 链节的链上抛装配错误，零链节构造才报「链为空」。Stream 的 nil 链节补 OnFailover 观测，消除与 Generate 的漂移。
- **测试**：`TestFailoverTailNilKeepsPriorError`（两路径同口径保留 429 sentinel + nil 信息）、`TestFailoverAllNilChainErrorCopy`（全 nil 链两路径文案一致）。
## #84 Client.Generate 单次尝试无独立限时
- **根因**：`retryPolicy.attemptTimeout` 字段早已预留且注释写明设计动机，但 `Client.retryPolicy()` 从不填——单客户端路径 `attemptCtx` 原样透传，挂死 provider（TLS 黑洞、网关半开）只能靠外层 ctx 兜底，首次尝试吞掉全部时间预算后 `MaxRetries`/退避沦为摆设。
- **修法**：`Client` 新增 `AttemptTimeout time.Duration`（0=不限，向后兼容），`retryPolicy()` 接线；每次物理尝试各自计时，不覆盖重试循环。doc 给出建议量级（60–120s）与最坏挂钟预算口径（≈ MaxRetries×AttemptTimeout + Σ退避）。
- **测试**：`TestClientAttemptTimeoutLimitsEachAttempt`（两次尝试各 ~40ms 独立计时收口）、`TestClientAttemptTimeoutZeroKeepsPassthrough`（0=不限，唯一挂钟约束来自外层 ctx）。
## #83 截断提升 MaxTokens×3/2 超出 fitInput 输出预留
- **根因**：`fitInput` 按 1 倍 `MaxOutputTokens` 预留裁剪输入；`finish_reason=length` 的重试轮提升输出上限 ×3/2 却不重裁输入——输入贴近窗口时 1.5 倍输出 + 原输入 > 窗口，二次尝试以 context too long 收场，还被判成确定性失败、在降级链上白烧一次切换。
- **修法**：`fitInput` 参数化为 `fitInputFor(outReserve)`（预算公式仍单源 `textutil.FitCharsLimit`），提升轮按新上限对已裁剪输入重算预算再裁一次（留痕）。
- **测试**：`TestClientTruncatedBoostRefitsInput`（800 rune 输入首轮不裁（945 预算）、提升轮裁到 742 预算并留痕）。
## #82 ContinueModel 续写合并以末轮 ResponseMeta 整体覆盖
- **根因**：续写最多 3 轮、每轮真实付费，但 `merged.ResponseMeta = next.ResponseMeta` 整体替换——首轮（输出大头）的 usage 从最终消息消失，按 `ResponseMeta.Usage` 记账的消费方对「截断+续写」只计末轮，成本系统性低估，恰在长输出（成本最高）场景失真。
- **修法**：`mergeContinueMeta`——FinishReason 以末轮为准；Usage 累加：prompt 取首轮（上下文按一份计）、completion 求和、total 重算 prompt+completion 保持自洽；末轮缺 usage 保住首轮不丢账；末轮无 meta 仍置 nil（防对无 meta 的模型反复续写）。
- **测试**：`TestContinueModelMergesUsageAcrossRounds`、`TestContinueModelUsageAccumulatesThreeRounds`、`TestContinueModelKeepsFirstUsageWhenLastMissing`。
## #77 续写派生超时吞错 —— 核实已修复 + 补回归测试
- **核实**：issue 引用的 `if ctx.Err() != nil || errors.Is(...) { return msg, ctx.Err() }` 是 #69 修复前旧代码；v0.20.0 起为分裂分支——父 ctx 取消返回 `ctx.Err()`，内层派生 deadline（`errors.Is(err, Canceled/DeadlineExceeded)`、外层 ctx 健康）包装上抛，不再返回 `(腰斩输出, nil)`。修复于 PR #73（ecc91f5）。
- **补测试**：wrap/continue_test.go 此前完全没有锁这两条分支——新增 `TestContinueModelDerivedDeadlineNotSwallowed`（派生 deadline 包装上抛且携带半截输出）、`TestContinueModelParentCancelPropagates`（父取消原样上抛）。
## #79 Failover Generate typed-nil panic —— 核实已修复
- **核实**：`generateModel` 自 #63（PR #72，01ae934）起改用 `isNilModel`（含 reflect typed-nil 判定），与 Stream/Resilient 单源；回归测试 `TestFailoverGenerateTypedNilSkipped` 在本分支复跑通过。issue 引用的「只判 `m == nil`」是修复前形态。
## 行为变更/迁移点（汇总）
- 数字带字母单位后缀的文案（`400ms` 等）不再命中状态码 marker——回到默认可重试。
- 全 nil 链的 Stream 错误文案从「failover 链为空」改为「链含 nil 模型」；链尾 nil + 前链真实失败时错误为 Join 聚合（`errors.Is/As` 可同时命中）。
- Client 配置 `AttemptTimeout` 后每次物理尝试独立限时（0 缺省行为不变）；GenerateJSON 直用 Client 时同样参与（Resilient 路径显式传 0，行为不变）。
- 截断提升轮输入会被进一步裁剪（多一处留痕）。
- 续写合并后的 usage 语义从「末轮整体覆盖」改为「首轮 prompt + 累加 completion」——记账数字上升是修正低估，非回归。
## 验证
`gofmt -l .` 空、`go vet ./...`、`go build ./...`、`go test ./...`（含 `-race`）本地全绿。

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.10.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.9.0` | `v0.10.0` |
| `github.com/oekit/concurrency` | `v0.25.1` | `v0.28.0` |
| `github.com/oekit/logx` | `v0.8.3` | `v0.8.5` |
| `github.com/oekit/pkg` | `v0.34.1` | `v0.37.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。


## [v0.20.0] — 2026-11-06

### 修复(wrap,obsx,usage)：provider deadline 上抛 / 流式 finish reason / 防重 doc 对齐（#69-#71）

llm#69——wrap/continue 的 ctx 类错误两分：调用方取消（ctx.Err() != nil）
原样上抛；provider 侧 deadline（外层 ctx 健康）包装后上抛——此前
errors.Is(DeadlineExceeded) 分支返回 ctx.Err()==nil，(半截, nil) 零错误
成功，半截内容直接进报告/工具参数。
llm#70——流式 UsageRecord.FinishReason 不再恒空：DrainStreamUsage 增第三
返回值（末帧 ResponseMeta.FinishReason），usage 侧与 obsx 的 logUsageEnd
（trace 字段补 finish_reason）同步透传——length 截断在流式观测面可见，
非流式/流式两路对称。
llm#71——NewUsageHandler doc 与 client.go 对齐：Budget **不在防重面**
（预算扣减不是遥测出口、不打标记），OnUsage+Budget+handler 三件套同时
装配会计两处——照旧 doc 装配的调用方此前静默双计。代码行为不变（本就
按 client.go 执行），修的是撒谎的那份文档。
全仓 -race 绿。
关联 oagentkit/llm#69 oagentkit/llm#70 oagentkit/llm#71
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>

### 修复(llm)：错误面六修——typed-nil 对称/marker 收窄/nil 语义/Timeout 生效面/mock 去宿主词/Labels 快照声明（#63-#68）

llm#63——generateModel 改用 isNilModel（与 Stream/Resilient 共用）：此前只查
接口 nil，typed-nil（(*OpenAIProvider)(nil) 装箱）在接口方法内解引用 panic，
「流式好使、非流式炸」的反直觉失效形态消除；注释「与 Stream 同契约」成真。
llm#64——marker 收窄（AGENTS 同族实弹教训：裸子串漏判方向=放弃自愈）：
- ctxLen 的裸 "too long" 收窄为 is/input/context + too long 邻接——网关超时
  「upstream took too long」不再被判成上下文超限
- det4xx 的裸 "does not exist" 收窄为 "model does not exist"——DNS 瞬时
  失败不再被判成模型不存在
- 未识别错误一律乐观可重试的 default 不变（宁误重试、勿弃自愈）
llm#65——ClassifyLLMError(nil) 改回 RetryHint{}：与同文件
RetryableLLMError(nil)=false 对齐（成功路径误传 nil 不再被当成「可重试的
失败」），doc 写明口径。
llm#66——decision.Config.Timeout 生效面显式声明：仅在未注入 HTTPClient 时
生效，注入方 own 超时（此前「配了不生效」无任何信号）。
llm#67——mock 去宿主领域词：MockEnv 值 BQ_DECISION_MOCK → DECISION_MOCK；
mockAnswers 的 route→fault-diagnosis 特判（bianque 路由名）移除，改为
Config.MockAnswers 按题名覆盖注入，通用确定性缺省（firstCandidate/中间档/
0.95）保持。
llm#68——WithUsageLabels doc 声明快照语义：注入即拷贝，晚定型归因必须
二次注入整包标签，漏注静默退化成组件类型口径。
测试：typed-nil 钉子 / 瞬态文案三点不撞 marker + 真错误仍命中 / nil 口径
翻转 / MockAnswers 覆盖优先 + 未覆盖走缺省。全仓 -race 绿。
关联 oagentkit/llm#63 oagentkit/llm#64 oagentkit/llm#65 oagentkit/llm#66 oagentkit/llm#67 oagentkit/llm#68
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>

### 重构(llm)：Limiter 消费面收窄成最小 Waiter 接口——宿主只实现 Wait(ctx)

v0.19.0 接口化方向正确，但接口面与消费面失配：client 全文只调
c.Limiter.Wait(ctx)，四方法面（Allow/AllowN/Reserve/Wait）迫使宿主自带
限速器时补齐三个永不被调用的方法——Reserve 的语义映射还没有合理答案
（接口「返回 0=立即可用」没有拒绝语义，x/time Reserve 有 ok=false 路径），
argus 适配时因此直接换 TokenBucket 而不是写适配器。
改动：
- 新增 Waiter 最小面（仅 Wait(ctx)），Client.Limiter / ResilientConfig.Limiter /
  wrap.RateModel.Limiter / wrap.NewRateModel 参数全部改用之——字段类型
  收窄成消费面是纯放宽：ratelimit.Limiter 实现隐式满足，存量传参零破坏
- llm/llm 兼容层镜像 Waiter（TestCompatParity 强制）
- 钉子测试：ratelimit.NewTokenBucketFloat 满足 Waiter；仅实现 Wait 的
  宿主限速器可赋值且在 oneAttempt 真实路径被消费；Wait 错误原样上抛
关联 oagentkit/llm#58
* style(llm): gofmt 对齐 Waiter 钉子测试
---------
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.9.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.8.0` | `v0.9.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.25.1

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.24.0` | `v0.25.1` |
| `github.com/oekit/logx` | `v0.8.2` | `v0.8.3` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.8.0

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.7.2` | `v0.8.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>

- 杂务(deps)：依赖地板追到最新——github.com/oekit/pkg v0.34.1

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/pkg` | `v0.34.0` | `v0.34.1` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.7.2

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oagentkit/textx` | `v0.7.1` | `v0.7.2` |
| `github.com/oekit/concurrency` | `v0.23.3` | `v0.24.0` |
| `github.com/oekit/pkg` | `v0.31.2` | `v0.34.0` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。
Co-authored-by: Murphy-Yi <zhangyi@Murphy-YideMac-Mini.local>


## [v0.19.0] — 2026-11-04

### 重构：限速器公开面改 oekit ratelimit.Limiter 接口型（BREAKING）

Client.Limiter / Resilient.Limiter / wrap.RateModel（含 NewRateModel 签名）从
*rate.Limiter（x/time/rate）改为 oekit ratelimit.Limiter 接口型——标准构造
ratelimit.NewTokenBucketFloat(rps, burst)，SlidingWindow 等其它实现可插；
Limiter.Wait(ctx) 调用点零改动。x/time 退出直接依赖（TokenBucket 内部仍包
x/time，经 oekit 传递 indirect）。翻案依据：v0.21.0 已补 Wait 阻塞档。
deps：concurrency v0.23.3/logx v0.8.2/pkg v0.31.2

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.23.2

| 依赖 | 现地板 | 抬到 |
|---|---|---|
| `github.com/oekit/concurrency` | `v0.23.1` | `v0.23.2` |
## 为什么
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`）要求：各仓 HEAD
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
oekit 基座与矩阵模块都在高频发版 ⇒ 地板是**跑步机**，落后一格就会判红。
## 兼容性实测
本工具在提交前跑完 `go build` / `go vet` / `go test -race -count=1 ./...`（全绿才提交）
与 `go mod tidy -diff`（无漂移）⇒ 改动**只影响依赖版本**，零行为差异。
只改 `go.mod` / `go.sum` ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oagentkit/textx v0.7.1

textx v0.7.1（2026-10-26 发布）此前一直没被抬 —— 本仓 HEAD 的新提交把这条**旧欠账**暴露在「发版时刻地板时效」门禁前 ⇒ 这次补上。
矩阵的**发版时刻地板时效**门禁（`verify_tag_floor --ref HEAD`，宽口径）要求：**各仓 HEAD**
（= 下次发版会发出去的内容）的依赖地板，相对那一刻的依赖最新版本，不能已落后。
兼容性实测：本仓只用这些依赖的少量 API ⇒ go build / go vet / go test -race 全绿，
**零行为差异**；go 指令未被顶起。
只改 go.mod / go.sum ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.23.1

oekit 基座又发了一版（`concurrency` v0.23.1，2026-10-29 02:19 CST）⇒ 上一轮抬到
v0.23.0 的地板已经落后 ⇒ 再抬一格。
本仓只用到 `concurrency` 的少量 API ⇒ 实测 go build / go vet / go test -race 全绿，
**零行为差异**；go 指令未被顶起。
只改 go.mod / go.sum ⇒ 不需要 CHANGELOG 条目（版本段由下次发版的 `prewrite` 生成）。

- 杂务(deps)：依赖地板追到最新——github.com/oekit/concurrency v0.23.0 github.com/oekit/logx v0.8.0 github.com/oekit/pkg v0.31.0

oekit 基座发了新版本（pkg v0.31.0 / concurrency v0.23.0 / logx v0.8.0）⇒ 本仓的
依赖地板落后 ⇒ 抬到最新，让**下一次发版**不再带出落后地板（打 tag 时刻的地板
必须是当时的最新——`verify_tag_floor` / `release.py doctor` 都在查这条）。
兼容性实测：本仓只用这些基座包的少量 API ⇒ go build / go vet / go test -race
全绿，**零行为差异**；go 指令未被顶起（本仓的 go 地板不动）。


## [v0.18.1] — 2026-10-25

- 杂务(deps)：抬基座依赖地板 —— oekit/concurrency v0.22.0 / oekit/pkg v0.28.0

## 现状
llm v0.18.0（2026-10-25T09:45Z）的基座地板钉在 `oekit/concurrency v0.20.2` /
`oekit/pkg v0.22.0`，而这两条依赖在**打 tag 之前**就已发布新版本：
| 依赖 | v0.18.0 钉的 | 当时最新 | 发布时间 |
| --- | --- | --- | --- |
| `github.com/oekit/concurrency` | v0.20.2 | v0.21.0 | 2026-10-25T01:26Z |
| `github.com/oekit/pkg` | v0.22.0 | v0.26.0 | 2026-10-25T09:21Z |
⇒ 发版那一刻地板就不是最新。tag 受 protect-tags ruleset 保护**不能重打**
（重打会让下游 `go.sum` 的 h1 硬失败），只能靠后续版本补齐 ——
欠账已按 `matrix-tools/scripts/tag_floor_debt.json` 的协议记入基线
（matrix-tools#134，还债路径见 matrix-tools#136）。
本 PR 抬到**当前**最新（基座仓仍在高频发版）：
- `github.com/oekit/concurrency` v0.20.2 → **v0.22.0**
- `github.com/oekit/pkg` v0.22.0 → **v0.28.0**
## breaking 面：逐条核实（不是照抄上游 CHANGELOG）
跨越 v0.22.0 ~ v0.28.0 这段里唯一的 BREAKING 是 **oekit/pkg v0.26.0** 的
`convert.ResolveClientIP` 去掉 `xri` 入参 —— 本仓与全矩阵**零调用**
（实测 grep：`ResolveClientIP` / `GetClientIP` 在 13 个模块 + 门面上均无命中）。
本仓实际用到的基座包只有 5 个，都不在任何 breaking 面上：
```
github.com/oekit/concurrency/async
github.com/oekit/concurrency/breaker
github.com/oekit/logx
github.com/oekit/pkg/httpjson
github.com/oekit/pkg/jsonrepair
github.com/oekit/pkg/mathx
```
- oekit/concurrency v0.21.0 的 BREAKING 是 `ratelimit.Limiter` 接口启用
  `Wait(ctx) error` —— 只影响**自行实现**该接口的调用方；本仓用的是
  `async` / `breaker`，全矩阵 `ratelimit.` **零命中**；
- oekit/pkg v0.23.0 ~ v0.28.0 其余各版均为新增/修复（cache 观测面、
  Raw base64 变体、shellx 转义、pagination 归一化等），**不含签名删除**；
- 全矩阵亦无 `oekit/pkg/cache` / `oekit/pkg/pagination` 的用法
  ⇒ v0.23.0 起的 cache 行为调整与 v0.25.2 起的 pagination 变更都碰不到本仓。
## 验证
```
gofmt -l .            # 无输出
go build ./...        # OK
go vet ./...          # OK
go test -race -count=1 ./...   # 全绿（7 个包，含 llm/llm 兼容层）
```
## 后续
本 PR 合并后走完整链：llm **v0.18.1**（预写待发版段 → 合入 → 在该 CHANGELOG 提交上
打 annotated tag）→ 门面 `agentkit` 跟到 v1.8.51 → 从 `tag_floor_debt.json` 移出
→ `sync-matrix` 刷矩阵源 → 拷站点副本（源先、副本后）。
关联 matrix-tools#136


## [v0.18.0] — 2026-10-25

### 新增(llm)：补 llm/llm 兼容层 —— 恢复 v0.7.0~v0.8.0 的历史 import 路径

llm 的根包在 v0.7.0 ~ v0.8.0 期间住在 llm/ 子目录，import 路径是
github.com/oagentkit/llm/llm。v0.9.0（2026-10-19）把根包上移到 module 根，
路径改成 github.com/oagentkit/llm，并标了 BREAKING CHANGE。
后果：钉在那四个版本上的下游升级时，一批 github.com/oagentkit/llm/llm 的
import 全部编译失败 —— 而它们用到的符号一个都没变，变的只是 import 路径
这一层。迁移成本是「每个引用文件改一行 import」，还可能与下游自己在做的
改动撞车。
本 PR 在 llm/llm 子目录补一层兼容包，把老路径重新提供一次：
- 类型用 type X = root.X（别名）—— 与根包是同一个类型，不是结构相同的两份；
- 函数用 var X = root.X 转发 —— 不写包装函数（包装要逐个复制签名，根包改
  签名时它会静默分叉；var 转发把签名交给编译器守着）；
- 常量原样转发；包级变量按值复制（与根包共享底层数据）。
镜像根包全部 71 个导出符号（31 type / 31 func / 2 var / 7 const）。
一致性由 llm/compat_test.go 的 AST 比对强制：根包增删导出符号而兼容层
没跟上，那条测试会红。另有三条守卫：
- TestCompatNoWrapperFunc：禁止写包装函数（签名会静默分叉）；
- TestCompatAliasIdentity：证明别名是同一个类型、转发是同一个函数值；
- TestCompatPackageIsImportable：目录名决定 import 路径末段，改了就等于
  把老路径又改没了。
兼容层是过渡期产物：只镜像根包已有符号、不新增能力，与门面仓一样只为存量
下游存在，随 0.x 版本线退役。新代码一律直接 import github.com/oagentkit/llm。
它不进矩阵的规范包清单：matrix_paths.new_import_path 把「包名末段 == 仓名」
收口到 module 根，登记它会让 versions.json 给两个包登记同一个 importPath。
矩阵里的记录位置是 modules.json 的 retiringPackages 账本（另开 PR）。
关联 #47
* fix(llm): 修 compat 层的 lint —— 废弃符号的转发与「别名同一性」断言
CI 的 Lint 是红的（SA1019 ×2 + QF1011 ×5 + 一处死代码），本 PR 因此合不了。
三类问题，处理方式各不相同：
1. **SA1019（已废弃符号的转发）**：`PriceOf` / `PriceOfMatched` 在根包已废弃
   （未配价返回 0 与「免费模型」不可区分；「最长前缀回落」会把同主干异价 SKU
   静默定价错）。但**兼容层必须转发它们**——下游钉在 v0.7.0~v0.8.0 时按旧路径
   引用，删掉就是编译断 ⇒ 保留转发、补 `Deprecated:` 说明（旧符号的坑一并带过来，
   让新代码别再碰）、加 `//nolint:staticcheck` 并写明理由。
2. **QF1011（显式类型可省略）**：那 5 行 `_ root.Client = Client{}` 看着像可简写，
   实则**显式根包类型就是断言本身**——「兼容层与根包是同一个类型」这条语义底线
   靠它表达（别名 ⇒ 编译期同一类型 ⇒ 可直接赋值）。简写成 `_ = Client{}` 就变成
   断言「Client 的类型是 Client」⇒ 恒真、断言消失 ⇒ 保留显式类型 + `//nolint`。
3. **死代码**：`exportSet.all()` 无调用点（`names()` 才在用）⇒ 删。
本地核验：gofmt 空 / go build / go vet / go test ./... 全绿 / golangci-lint 0 issues。

- 文档(llm)：刷正 README 的外部依赖清单 —— 与 go.mod 实测对账

本仓 README 的「外部依赖」清单与**各仓 go.mod 的事实**对不上。
矩阵的单一数据源 `matrix-tools/modules.json` 也有同样漂移，已在
[matrix-tools#126](https://github.com/oagentkit/matrix-tools/pull/126) 同步刷正。
## 现状核实
```bash
$ grep "eino-ext" llm/go.mod
7:	github.com/cloudwego/eino-ext/components/model/openai v0.1.13
23:	github.com/cloudwego/eino-ext/libs/acl/openai v0.1.17 // indirect
$ grep -c "^github.com/cloudwego/eino-ext v" llm/go.sum
0
```
⇒ eino-ext 已按组件拆成独立 module，本仓直接依赖的是 **`components/model/openai`** 那个；
根模块 `github.com/cloudwego/eino-ext` **连 go.sum 都没有** ⇒ README 列的是一个**不存在的模块路径**。
## 影响
读者拿 `github.com/cloudwego/eino-ext` 去 `go get` 会拿到一个本仓根本没用的模块路径；反过来，真正的直接依赖 `eino-ext/components/model/openai` 反而没被列出来。
## 改动
**只改清单那一行（`- `github.com/cloudwego/eino-ext`` ⇒ `- `github.com/cloudwego/eino-ext/components/model/openai``）**，其余段落逐字未动。
## 验收
- [x] 清单与 `go.mod` 实测一致
- [x] 与 `matrix-tools/modules.json` 的登记一致（同期在 matrix-tools#126 刷正）
- [x] **纯 .md 改动** ⇒ 不需要 CHANGELOG 条目、不打 tag、不发版（无发版步骤）
关联 oagentkit/matrix-tools#125


## [v0.17.1] — 2026-10-23

- 杂务(deps)：oekit 依赖地板抬到 v0.20.2（concurrency）

抬 go.mod 的依赖地板，跟到基座（oekit）本轮新版本：
- github.com/oekit/concurrency  v0.20.1 -> v0.20.2
本仓自身代码零改动，只改 go.mod / go.sum。
下游要做什么：**go mod tidy**——地板抬高对下游是**隐式破坏性变更**：
不跑就报 updates to go.mod needed，且报错不提真正原因。
为什么抬：本仓 tip（ab7c8125d，2026-10-23T06:39:43Z）的地板还钉在 v0.20.1，
而基座那边 concurrency 最新已是 v0.20.2（发布于 2026-10-23T03:09:44Z）——
比 tip 早 3.5 小时。矩阵的「发版时刻地板时效」门禁
（verify_tag_floor.py --org oagentkit，默认 HEAD 口径）报红：
下一次打 tag 就会把落后地板发出去，而 tag 受 protect-tags 保护不可重打。
为什么无行为差异：上游 concurrency v0.20.1 -> v0.20.2 的 diff 里**没有任何 .go 文件**
（只有它自己的 go.mod 把 oekit/pkg 从 v0.15.1 抬到 v0.22.0，另有文档/CI 文件）——
本仓唯一 import 的 async/ 与 breaker/ 子包一字未动。且本仓已直接钉
oekit/pkg v0.22.0 = 上游 v0.20.2 自己要求的版本，MVS 解析结果不变，
连传递依赖都不会移位。
门禁：go build / vet / test -race 通过，gofmt -l 空，go mod tidy -diff 空。
Closes #43


- 文档(decision)：补「题面规范」——criteria 描述必须写类别特征，不能写动作指令

实测（2026-10-23，自托管 Laya 0.4.13，59 条中文用例）：同一个二分类任务，
**决定判定质量的是描述写法，不是任务、也不是候选顺序**：
- 动作指令式（「可以直接回答的概念/知识问句」/「需要派专家做诊断的诉求」）
  ⇒ 准确率 **12.7%**，且 63/63 全判同一个标签（**退化成常量输出**）：模型把标签
  读成了「要执行的动作」，于是恒选「直接回答」。
- 类别特征式（「询问概念、原理、定义、含义…」/「描述故障、异常、容量、性能…」）
  ⇒ 准确率 **81.0%**（门槛 0.6 档 92.7%），且候选顺序对调结果**完全相同**
  （**不顺序敏感**）。
⇒ 规范：**候选的描述要说明「这类输入长什么样」，不要说明「对它做什么」**。
「要不要派专家」「要不要直接回答」是**动作**，写进描述会让模型按动作匹配；
「询问概念原理」「描述故障容量」是**类别特征**，才是模型要的判据。
这是**契约级**知识（所有宿主共用），故写进客户端包文档（问法的载体）；
配套的判定面公共契约在 agent-harness 的 protocol/decision.go。


## [v0.17.0] — 2026-10-23

### 新增(cost)：CostRecord/记账入口/Client 回调补前缀缓存命中量（cached）

CachedTokens（DeepSeek/MiMo 等端点的 PromptTokenDetails.CachedTokens）此前被
usage.go 采集却在记录层/回调层静默丢弃：缓存命中率不可观测、成本缓存折扣无处表达。
纯向后兼容加法（不碰 Price 定价哲学——缓存命中价按厂商规则、由调用方覆盖）：
- CostRecord.CachedTokens：观测入库，不改 CostOf 计价口径
- RecordPricedCached/RecordAutoCached/RecordUnpricedCached：带 cached 的记账入口，
  旧三方法委托（cached=0，单源）；clampCached 钳 [0,prompt] 防坏数据污染命中率
- ModelSummary.TotalCachedTokens + CacheHitRatio() = Σcached/Σprompt
- Client.OnUsageDetailed(stage, UsageRecord)：与 OnUsage 并行、含 cached；
  account() 从 PromptTokenDetails.CachedTokens 采集（估算路径留 0）
缓存折扣成本仍由调用方在 Price 层按厂商规则表达（见 Price 文档），本 PR 只补数据面。
Closes #37

## [v0.16.0] — 2026-10-23

### 新增(decision)：新增 System One 协议客户端（非自回归判定模型）

本仓此前只覆盖自回归 chat 模型（降级链/预算/成本/分阶段路由/主备）。另一族
非自回归判定模型（System One 协议：Jev 托管、Laya 自托管、Kev、Clef、CLM）形态
完全不同：没有 messages 概念、不生成散文、只做三原语判定（choice 选候选 /
score 打有序档位 / noul 是否），输出有限标签 + 概率分布 + 校准置信度，一条状态
一次前向 pass 答完全部问题。它进不了 chat 那套形状，故单独立包 decision。
- 协议：POST {base}/v1/systemone，Authorization: Bearer。Jev（托管）与 Laya
  （自托管）同协议同载荷 ⇒ 换端点即换模型，本包只实现一套协议。
- 置信口径由本包单源（Answer.EffectiveConfidence）：真机实测两后端的 confidence
  给法不一致（Laya choice 自有公式 0.9013 + 另给 x_jev_confidence 0.9712；
  Laya noul 直接给 p 0.8125，Jev 口径是 |2p−1| 0.625）——两端各自自洽、都不报错，
  但同一门槛在两端含义漂移。故按 Jev 公开公式从概率分布本地算（choice
  (N·p_top−1)/(N−1)、score max(0,1−Σpᵢ|i−m|/MAD)、noul |2p−1|），分布缺失才
  回落端点值。本地算法与 Laya 的 x_jev_confidence 逐位一致（实测 0.9712/0.6769）
  ⇒ 公式被后端实现交叉验证过。
- legend 以真机为准是对象（官方文档样例写数组，真机返回对象，数组形态解析直接
  失败）；后端专有字段 answer_confidence/x_jev_confidence/action 解析并保留。
- 不吞错：端点不可达/超时/非 2xx 一律返错（回落是调用方的义务）。
- BQ_DECISION_MOCK=1 mock 模式（验接线不验质量）；Warmup 预热（端点首启加载
  检查点实测数十秒~数分钟，不预热则首个真实请求必然撞超时 ⇒ 静默假生效）。
门禁：gofmt -l（空）/ go vet / go build ./... / go test -race -count=1 ./decision/ 全过；
真机用例在 decision/decision_live_test.go（build tag decision_live），可复跑。

- 杂务(deps)：依赖地板跟到发版时刻的最新——oekit/concurrency v0.20.1、oekit/pkg v0.22.0

打 tag 时刻的依赖地板必须是当时的最新（release.py doctor 对本仓判红 2：
concurrency 钉在 v0.19.0 而远端最新 v0.20.1；pkg 钉在 v0.20.0 而远端最新 v0.22.0）。
⚠️ 地板抬高对下游是**隐式破坏性变更**：不跑 `go mod tidy` 的下游会构建红，
报错是 `go: updates to go.mod needed`——不提真正原因。该事实由
matrix-tools/scripts/gen_floor_bumps.py 按 go.mod 差异结构化登记进 versions.json。
本地验证：gofmt -l（空）/ go vet / go build ./... / go test -race -count=1 ./... 全过。


## [v0.15.2] — 2026-10-21

- 杂务(deps)：依赖地板跟到 textx v0.6.2

## 下游要做什么
**`go mod tidy`**。地板抬高对下游是**隐式破坏性变更**：不跑就报
`updates to go.mod needed`，且报错不提真正原因（**不是本仓的 API 变了**）。
本仓**自身代码零改动**——只看本仓的 diff 看不出这是一次地板抬高。
## 为什么抬
上一版 tag（v0.15.1）打出去时，它依赖的 textx 的**新版已经先发出**了
（textx v0.6.2 早于本 tag 数十小时）——即「打 tag 那一刻地板就落后」。
矩阵的「发版时刻地板时效」门禁（`verify_tag_floor.py --org oagentkit --latest-only`）
当场报红：
```
- llm@v0.15.1 / github.com/oagentkit/textx：地板 v0.6.1，发版时刻最新已是 v0.6.2
```
门禁给的修法是「抬 go.mod 地板 → go mod tidy → 重新发 tag」——本 PR 就是那条修法。
## 为什么无行为差异
textx v0.6.1 → v0.6.2 本身也是**纯依赖地板追平**（textx 自身代码零改动，
只抬了 `oekit/pkg` v0.18.0→v0.20.0），本仓用到的 textx 能力与行为不变。
## 门禁
```
go build / vet / test -race   ✅
gofmt -l .                    ✅ 空
```


## [v0.15.1] — 2026-10-21

- 与 v0.15.0 相比无代码差异（版本号/打包变更）

## [v0.15.0] — 2026-10-21

### 新增(openai)：OpenAIProviderConfig 支持注入 HTTPClient——宿主的拨号层守卫接得上

## 下游要做什么
**什么都不用做**——纯增量：字段缺省为 nil 时行为**逐字不变**，现有调用方不需要改。
想给 LLM 出站挂守卫的宿主，改一行：
```go
hc, err := netguard.NewClient(nil, netguard.Allow{Private: true, Loopback: true})
cfg.HTTPClient = hc   // hc.Transport 挂了 netguard.DialFunc
```
（`netguard` 在 agent-harness 仓；这里是说明接线形态，本仓不依赖它。）
## 缺口
`OpenAIProviderConfig` 没有 `HTTPClient` 字段，宿主**无法**把自己构造的
`http.Client` 注入 LLM 请求链路。而宿主的出站有一条硬要求：**拨号层必须挂守卫**。
写面校验（按 URL 判）拦不住 DNS rebinding——两次解析之间权威 DNS 可以改口：
```
写面校验：evil.example → 1.2.3.4        （公网，放行）
实际请求：evil.example → 169.254.169.254 （元数据段，已不在判定面内）
```
要闭合它，守卫必须挂在 `Transport.DialContext` 上，这要求宿主能提供
**自己的 `*http.Transport`**（或整个 `*http.Client`）。而 LLM 档案的
`base_url` 是**用户可配**的（平台代发 LLM 请求的目标），是这条链上暴露最大的面。
## 语义：显式注入优先于内部构造
| 宿主注入 | 捕获 Retry-After | 行为 |
|---|---|---|
| 否 | 否 | 返回 nil——走 openai 包内部缺省（**既有行为逐字不变**）|
| 否 | 是 | 自建 client（**既有行为逐字不变**）|
| 是 | 否 | 原样使用宿主注入的 client |
| 是 | 是 | **在宿主注入的 Transport 外面**套一层 `RetryAfterTransport` |
最后一行是此前**做不到**的组合：自建 client 走的是 `http.DefaultTransport`，
宿主的守卫挂不上去——`CaptureRetryAfter` 与自定义守卫此前是**互相排斥**的。
现在两条路径合流。
派生时**复制**一份 client，不就地改写宿主传进来的那个（宿主可能还在用它）。
超时语义：注入后由注入的 Client 完全接管，本仓不再叠加自己的 Timeout
（eino 侧同一口径——设了 `HTTPClient` 就不再用 `config.Timeout`）。
## 测试
- 四条路径的取舍，逐条断言（含「不改宿主 client」）；
- **端到端**：httptest + 计数型 Transport，断言注入的 Client 真的承担了出站
  请求——注入点装在 config 上，中间隔着 fillDefaults → httpClient →
  chatModelConfig → eino NewChatModel 四跳，任何一跳把它丢了，单测全绿而
  **守卫根本没挂上**（宿主以为出站有保护，实际裸奔）。
- **反向验证**：把实现改成「看不见注入」，端到端用例变红并点名
  「注入的 Client 没被使用——出站请求没走宿主的 Transport」。
## 顺带抬的地板
```
oekit/pkg        v0.18.0 → v0.20.0
oekit/concurrency v0.18.0 → v0.19.0
```
对本仓**零行为差异**：本仓只 import `concurrency/async` 与 `concurrency/breaker`，
这两个包在 v0.18.0→v0.19.0 之间**一字未动**（改动在 `retry` 与 `syncx`，
本仓不 import）；`oekit/pkg` 的变更见 agent-harness v0.8.2 段（纯增量 +
一处退避溢出修复）。
## 门禁
```
go vet ./...        ✅
go test -race ./... ✅（5 包全绿）
gofmt -l .          ✅ 空
golangci-lint       ✅ 0 issues
```

- 文档：修正陈旧的矩阵内依赖声明

本仓的矩阵内依赖实际只有 `textx`（go.mod 只 require 它，代码也只 import 它）。
`obs` 与 `runtime` 是**陈旧声明**——它们曾被依赖（`obsx` 迁入本仓、`breaker` 委托
`oekit/concurrency` 之后就不再引用了），但 `modules.json` 的 `dependsOn` 一直没跟上。
现在有一条机器门禁专门对账这件事（`matrix-tools/scripts/verify_import_direction.py`：
真实 import ⊆ 声明），它把本仓标成了「声明陈旧」。
声明与事实不一致的代价：所有以声明图为输入的门禁（依赖闭包、层级校验）
都在一张漂了的图上做校验；矩阵的机器可读源也对外播着一个不存在的依赖。
本 PR 把声明与手写文档里那一行一起改对，与生成器（`gen_docs.py` 的
`dep_line`）的输出格式保持一致。矩阵源侧的修正在
oagentkit/matrix-tools 的对应 PR 里。

- 文档(AGENTS)：补「发版顺序」小节，指向 org CONTRIBUTING 单一副本

13 个业务仓的 AGENTS.md 此前对发版顺序零覆盖（只有 textx 有个
「### 版本与发版」标题，但里面只讲 SemVer 与 module 路径），
发版顺序只写在 org 规则仓的 .github/CONTRIBUTING.md 里，单仓读者看不到。
本次只加**指针 + 本仓相关提醒**，runbook 本体保持**单一副本**
（CONTRIBUTING §发版），避免 13 份副本各自漂移。
覆盖四条：
- CHANGELOG.md 由 matrix-tools/scripts/gen_changelogs.py 从 git 历史生成，不要手改
- tag 必须落在 CHANGELOG 提交之后（下游 go get 拿到的是 tag 快照）
- 警告：无参重生成会吞掉待发版段，--next-version 是唯一能产出待发版段的开关
- 本仓相关提醒（见正文）


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
