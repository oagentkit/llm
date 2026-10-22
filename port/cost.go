package port

import (
	"sync"
)

// CostTracker token 成本追踪器。
// 按模型+阶段统计 token 消耗和成本。
// 环形缓冲：满窗后 Record O(1) 覆盖最旧，不再每笔整体搬移。
//
// # 价格从哪来
//
// 三种记录入口，价格来源不同，**但「价格未知」在任何一条路径上都不会被记成 0**：
//
//   - RecordAuto：按价表自动查（内建价 + 调用方覆盖）——**推荐**。
//   - RecordPriced：调用方显式给单价（企业折扣价、代理加价率）。
//   - Record：调用方每次传单价（**已废弃**，单位是每 1K token，与 Price 的每 1M 差一个数量级）。
//
// 查不到价格时记录仍会写入（用量数据不丢），但会被标记为 PriceUnpriced，
// 可经 Unpriced() / UnpricedCount() 查出来——**0 成本与「价格未知」必须可区分**。
type CostTracker struct {
	mu      sync.Mutex
	records []CostRecord // 定长环形（len==cap==maxCostRecords 后进入环形态）
	head    int          // 下一写入位 / 最旧记录位
	full    bool         // 环形是否已绕回

	pricing *PriceTable // 价表（只读，构造后不变）
}

// CostRecord 单次调用成本记录。
type CostRecord struct {
	Model            string // 模型名
	Stage            string // 阶段（R1/R2/R3/R4/R5/plugin:xxx）
	PromptTokens     int
	CompletionTokens int
	// CachedTokens 命中前缀缓存的 prompt token（⊆ PromptTokens；DeepSeek/MiMo 等
	// 端点的 PromptTokenDetails.CachedTokens）。只作观测——不改 CostUSD 计价口径
	//（缓存折扣是厂商规则、由调用方在 Price 层覆盖，见 Price 文档）；缓存命中率
	// = ΣCachedTokens / ΣPromptTokens，从 Summary/ModelSummary 派生。零值 = 未知。
	CachedTokens int
	CostUSD      float64 // 本次成本（美元）

	// PriceStatus 计价状态：PricePriced / PriceUnpriced。
	//
	// ⚠️ PriceUnpriced 时 CostUSD 是 **0**，但那表示「**价格未知**」而不是
	// 「免费」——真实成本无从得知。判读汇总口径时必须同时看
	// ModelSummary.UnpricedCalls：TotalCostUSD==0 且 UnpricedCalls>0
	// ⇒ 成本口径不完整，不是「这个模型不要钱」。
	PriceStatus PriceStatus
	// PriceKey 命中的价表键（PricePriced 时非空；PriceUnpriced 时为空）。
	PriceKey string
	// PriceMatched 命中方式（exact / normalized；未命中为 none）。
	PriceMatched MatchKind
}

// NewCostTracker 创建成本追踪器（用内建价表）。
func NewCostTracker() *CostTracker {
	return NewCostTrackerWithTable(DefaultPriceTable())
}

// NewCostTrackerWithTable 创建成本追踪器（指定价表）。
//
// 价表是**只读**的：可构造一份带覆盖的表传进来
// （NewPriceTable(overrides)），也可在运行期用 PriceTable.WithOverride 造新表
// 再换一台 tracker。
func NewCostTrackerWithTable(table *PriceTable) *CostTracker {
	if table == nil {
		table = DefaultPriceTable()
	}
	return &CostTracker{pricing: table}
}

// maxCostRecords 单进程保留的最大记录数（长驻进程防无界增长；超限丢最旧，
// Summary 汇总数字随之只反映窗口内记录）。
const maxCostRecords = 10_000

// Record 记录一次调用（**调用方自带单价，每 1000 token**）。
//
// Deprecated: 单位是「每 1K token」，而 Price/RecordPriced 是「每 1M token」——
// 同一仓里并存两套单位是一个数量级的错账来源。改用 RecordPriced（显式单价，
// 单位写在类型上）或 RecordAuto（按价表自动计价）。
func (ct *CostTracker) Record(model, stage string, promptTokens, completionTokens int, costPer1K [2]float64) {
	ct.append(CostRecord{
		Model:            model,
		Stage:            stage,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CostUSD:          costOf(promptTokens, completionTokens, costPer1K),
		PriceStatus:      PricePriced, // 调用方显式给了价
		PriceMatched:     MatchExact,
	})
}

// RecordPriced 记录一次调用，单价由调用方显式给出（**美元 / 每 1M token**）。
//
// 适用：企业折扣价、代理转发加价率、人民币计价合同的折算价——
// 这些都**不属于**内建价表能覆盖的范围。
func (ct *CostTracker) RecordPriced(model, stage string, promptTokens, completionTokens int, price Price) {
	ct.RecordPricedCached(model, stage, promptTokens, 0, completionTokens, price)
}

// RecordPricedCached 记录一次调用（带前缀缓存命中量）。
//
// cachedTokens 是命中前缀缓存的 prompt token（⊆ promptTokens；DeepSeek/MiMo 等端点的
// PromptTokenDetails.CachedTokens）。**缓存折扣不在本层算**——Price 文档已明「缓存
// 命中价按厂商规则、由调用方覆盖」，故计价仍用 price.CostOf(promptTokens, completion)
// 的同一口径；cachedTokens 只作观测入库（CostRecord.CachedTokens），供缓存命中率派生。
// 需要缓存折扣成本的调用方，在 Price 层按厂商规则折算后传入（或自行摊薄 InputPerM）。
func (ct *CostTracker) RecordPricedCached(model, stage string, promptTokens, cachedTokens, completionTokens int, price Price) {
	ct.append(CostRecord{
		Model:            model,
		Stage:            stage,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     clampCached(promptTokens, cachedTokens),
		CostUSD:          price.CostOf(promptTokens, completionTokens),
		PriceStatus:      PricePriced,
		PriceMatched:     MatchExact,
	})
}

// RecordAuto 按价表自动计价并记录一次调用。
//
// **查不到价格不报错，但也绝不静默记 0**：记录照写（用量数据不丢），
// 状态标为 PriceUnpriced，调用方可用 Unpriced() / UnpricedCount() 查出来，
// 汇总里也会在 ModelSummary.UnpricedCalls 上体现。
//
// 需要「查不到就硬失败」的路径用 LookupPriceStrict。
func (ct *CostTracker) RecordAuto(model, stage string, promptTokens, completionTokens int) {
	ct.RecordAutoCached(model, stage, promptTokens, 0, completionTokens)
}

// RecordAutoCached 按价表自动计价并记录一次调用（带前缀缓存命中量）。
// cachedTokens 语义同 RecordPricedCached：只作观测入库，不改计价口径。
func (ct *CostTracker) RecordAutoCached(model, stage string, promptTokens, cachedTokens, completionTokens int) {
	price, lk := ct.pricing.Lookup(model)
	if !lk.Found() {
		ct.RecordUnpricedCached(model, stage, promptTokens, cachedTokens, completionTokens)
		return
	}
	ct.append(CostRecord{
		Model:            model,
		Stage:            stage,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     clampCached(promptTokens, cachedTokens),
		CostUSD:          price.CostOf(promptTokens, completionTokens),
		PriceStatus:      PricePriced,
		PriceKey:         lk.Key,
		PriceMatched:     lk.Kind,
	})
}

// RecordUnpriced 记录一次调用，并显式标记为**价格未知**。
//
// 与 RecordAuto 查不到价时走的同一条路：CostUSD 是 0，但那表示「未知」不是
// 「免费」——记录上的 PriceStatus=PriceUnpriced 与汇总里的 UnpricedCalls
// 就是它的标记，运维据此告警，而不是对着恒为 0 的看板误判模型免费。
func (ct *CostTracker) RecordUnpriced(model, stage string, promptTokens, completionTokens int) {
	ct.RecordUnpricedCached(model, stage, promptTokens, 0, completionTokens)
}

// RecordUnpricedCached 记录一次调用（价格未知，带前缀缓存命中量）。
// cachedTokens 只作观测入库——价格未知不影响缓存命中率可算。
func (ct *CostTracker) RecordUnpricedCached(model, stage string, promptTokens, cachedTokens, completionTokens int) {
	ct.append(CostRecord{
		Model:            model,
		Stage:            stage,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     clampCached(promptTokens, cachedTokens),
		PriceStatus:      PriceUnpriced,
		PriceMatched:     MatchNone,
	})
}

// clampCached 归一化缓存命中量：钳到 [0, promptTokens]（cached ⊆ prompt 契约），
// 负值归 0——坏数据不污染命中率派生。
func clampCached(promptTokens, cachedTokens int) int {
	if cachedTokens <= 0 {
		return 0
	}
	if cachedTokens > promptTokens {
		return promptTokens
	}
	return cachedTokens
}

// append 追加一条记录（环形缓冲，满窗后 O(1) 覆盖最旧）。
func (ct *CostTracker) append(rec CostRecord) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if len(ct.records) < maxCostRecords {
		ct.records = append(ct.records, rec)
		if len(ct.records) == maxCostRecords {
			// 切满后转环形：cap 预留到位，后续 O(1) 覆盖
			ct.records = ct.records[:maxCostRecords:maxCostRecords]
			ct.head = 0
			ct.full = true
		}
		return
	}
	// 环形覆盖最旧
	ct.records[ct.head] = rec
	ct.head = (ct.head + 1) % maxCostRecords
}

func costOf(promptTokens, completionTokens int, costPer1K [2]float64) float64 {
	return float64(promptTokens)/1000*costPer1K[0] + float64(completionTokens)/1000*costPer1K[1]
}

// Records 返回所有记录的副本（时间序：最旧在前）。
func (ct *CostTracker) Records() []CostRecord {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	n := len(ct.records)
	if !ct.full {
		out := make([]CostRecord, n)
		copy(out, ct.records)
		return out
	}
	// 环形：从 head（最旧）展开
	out := make([]CostRecord, 0, maxCostRecords)
	out = append(out, ct.records[ct.head:]...)
	out = append(out, ct.records[:ct.head]...)
	return out
}

// Unpriced 返回**价格未知**的记录（时间序：最旧在前）。
//
// 这是「未知价不静默」的可观测出口：账面上 CostUSD 是 0，但这些调用
// 根本没计价——运维能据此告警，而不是对着一个恒为 0 的看板以为模型免费。
func (ct *CostTracker) Unpriced() []CostRecord {
	all := ct.Records()
	out := make([]CostRecord, 0)
	for _, r := range all {
		if r.PriceStatus == PriceUnpriced {
			out = append(out, r)
		}
	}
	return out
}

// UnpricedCount 返回价格未知的记录数。
func (ct *CostTracker) UnpricedCount() int {
	return len(ct.Unpriced())
}

// Summary 按模型汇总。
func (ct *CostTracker) Summary() map[string]ModelSummary {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	m := map[string]ModelSummary{}
	for _, r := range ct.records {
		s := m[r.Model]
		s.Model = r.Model
		s.TotalPromptTokens += r.PromptTokens
		s.TotalCompletionTokens += r.CompletionTokens
		s.TotalCachedTokens += r.CachedTokens
		s.TotalCostUSD += r.CostUSD
		s.CallCount++
		if r.PriceStatus == PriceUnpriced {
			s.UnpricedCalls++
		} else {
			s.PricedCalls++
		}
		m[r.Model] = s
	}
	return m
}

// ModelSummary 单模型汇总。
type ModelSummary struct {
	Model                 string
	TotalPromptTokens     int
	TotalCompletionTokens int
	// TotalCachedTokens 命中前缀缓存的 prompt token 累计（⊆ TotalPromptTokens）。
	TotalCachedTokens int
	TotalCostUSD      float64
	CallCount         int
	// PricedCalls 已计价的调用数。
	PricedCalls int
	// UnpricedCalls **价格未知**的调用数——这些调用的成本没进 TotalCostUSD。
	//
	// 判读口径：TotalCostUSD==0 且 UnpricedCalls>0 ⇒ 成本口径不完整
	//（不是「这个模型免费」）；TotalCostUSD==0 且 UnpricedCalls==0 ⇒ 确实免费/零价。
	UnpricedCalls int
}

// TotalTokens 总 token 消耗。
func (s ModelSummary) TotalTokens() int {
	return s.TotalPromptTokens + s.TotalCompletionTokens
}

// CacheHitRatio 前缀缓存命中率 = TotalCachedTokens / TotalPromptTokens。
// 无 prompt（或零命中）返回 0。命中率高 = 上下文前缀稳定（provider context cache
// 前提是字节稳定）；命中率异常低 = 提示词/工具表前缀在抖动，值得查。
func (s ModelSummary) CacheHitRatio() float64 {
	if s.TotalPromptTokens <= 0 {
		return 0
	}
	return float64(s.TotalCachedTokens) / float64(s.TotalPromptTokens)
}
