# llm CHANGELOG

> 本文件由 `matrix-tools/scripts/gen_changelogs.py` 从 git 历史生成，按 tag 切段、取每段内的 conventional commit 渲染。

## [v0.11.0] — 2026-10-19

- 杂务(deps)：依赖地板对齐远端最新（obs v0.7.0 / textx v0.5.0） (13)

## [v0.10.0] — 2026-10-19

- 杂务(deps)：依赖地板对齐远端最新（obs v0.5.0 / textx v0.4.0 / concurrency v0.17.1 / pkg v0.15.1 / logx v0.7.1） (#13)

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

- 杂务(deps)：依赖地板升到「历史最新 + 0.1.0」 (#4)
- 杂务(deps)：跟进最新 tag (#3)
- 杂务(deps)：oekit 依赖升级 (#2)
- 杂务(deps)：跨仓依赖升级 (#1)
- 杂务(deps)：oekit/concurrency v0.6.0 -> v0.9.0

- CI：接入 org 共享门禁（规则本体在 oagentkit/.github）
