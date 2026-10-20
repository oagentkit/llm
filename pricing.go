package llm

import (
	"github.com/oagentkit/llm/port"
)

// 本文件只做**类型别名与薄封装**——实现已下沉到 github.com/oagentkit/llm/port。
// 理由见 budget.go 的说明：价表本身不依赖 eino，却原先住在依赖 eino 的根包里，
// 非 eino 生态的宿主因此连「按价表算成本」都做不到——而这正是 llm#9 那条
// 成本线的目的（价表随包维护，下游自动受益）。
type (
	// Price 单价（美元 / 每 100 万 token）。
	Price = port.Price
	// PriceStatus 计价状态。
	PriceStatus = port.PriceStatus
	// MatchKind 查价命中方式。
	MatchKind = port.MatchKind
	// PriceLookup 查价结果：命中的键 + 命中方式。
	PriceLookup = port.PriceLookup
	// PriceTable 价表：内建价 + 调用方覆盖。
	PriceTable = port.PriceTable
)

const (
	// PricePriced 已按单价算出成本。
	PricePriced = port.PricePriced
	// PriceUnpriced 价格未知（**不等于成本为 0**）。
	PriceUnpriced = port.PriceUnpriced

	// MatchNone 未命中（价格未知）。
	MatchNone = port.MatchNone
	// MatchExact 原名精确命中。
	MatchExact = port.MatchExact
	// MatchNormalized 归一化后命中（去部署前缀 / 去日期后缀）。
	MatchNormalized = port.MatchNormalized
)

// ErrPriceUnknown 价表里查不到该模型。
var ErrPriceUnknown = port.ErrPriceUnknown

// DefaultPriceTable 返回内建价表（无覆盖）。
func DefaultPriceTable() *PriceTable { return port.DefaultPriceTable() }

// NewPriceTable 造一份价表：内建价 + overrides 覆盖。
//
// 覆盖键与内建表走**同一套归一化规则**（折算成基座形态再入库），所以覆盖
// `mimo-v2.5-pro` 与覆盖 `mimo-v2.5-pro-20250101` 是等价的——同一基座上的
// 多个覆盖键按固定的优先级规则只留一个生效（详见 port.NewPriceTable 的文档）。
func NewPriceTable(overrides map[string]Price) *PriceTable { return port.NewPriceTable(overrides) }

// LookupPrice 用内建价表查价。
func LookupPrice(model string) (Price, PriceLookup) { return port.LookupPrice(model) }

// LookupPriceStrict 查价，查不到返回 ErrPriceUnknown。
func LookupPriceStrict(model string) (Price, error) { return port.LookupPriceStrict(model) }

// PriceOf 按模型名匹配定价表估算单次调用成本（USD；未配价返回 0，不阻塞记账）。
//
// Deprecated: 未配价时返回 0，与「免费模型」不可区分——这正是本仓要根除的静默失真。
// 改用 PriceTable.Lookup / LookupPrice：查不到时是「价格未知」而非 0。
//
// 另注意其「最长前缀回落」会把同主干异价 SKU 静默定价错（gpt-4o-mini 命中
// gpt-4o），新查价 API 不再提供该行为。
func PriceOf(pricing map[string]Price, model string, prompt, completion int) float64 {
	return port.PriceOf(pricing, model, prompt, completion)
}

// PriceOfMatched PriceOf 的可观测形态：返回命中的定价键与是否经前缀回落。
//
// 前缀回落对「同主干异价 SKU」（gpt-4o 前缀命中 gpt-4o-mini）会静默误定价
// ——宿主可在装配期对回落命中做校验/上报（第十轮审计）。
//
// Deprecated: 同 PriceOf——未配价返回 0 不可区分「免费」，前缀回落会误定价。
// 改用 PriceTable.Lookup（返回 PriceLookup，命中方式可见且不做前缀回落）。
func PriceOfMatched(pricing map[string]Price, model string) (Price, string, bool) {
	return port.PriceOfMatched(pricing, model)
}

// providerPrice 解析某个 Provider 的单价。
//
// 优先级（**显式永远压过查表**）：
//
//  1. PricedProvider.ModelPricing() 显式单价（美元 / 每 1M）——折扣价、加价率、折算价；
//  2. Provider.CostPer1KTokens() 非零（每 1K）——历史显式口径，内部换算到每 1M；
//  3. 按模型名查价表（table；nil = 内建价表）。
//
// 三者都没有 ⇒ (Price{}, false)，**价格未知**。
// 注意第 2 条的 0 与第 3 条的未命中都归到「未知」：把两者都当 0 记，
// 就是本仓要根除的静默失真（0 成本与免费模型不可区分）。
func providerPrice(p Provider, table *PriceTable) (Price, bool) {
	if p == nil {
		return Price{}, false
	}
	if pp, ok := p.(PricedProvider); ok {
		if price, ok := pp.ModelPricing(); ok {
			return price, true
		}
	}
	in1K, out1K := p.CostPer1KTokens()
	if in1K != 0 || out1K != 0 {
		// 每 1K → 每 1M（×1000）。
		return Price{InputPerM: in1K * 1000, OutputPerM: out1K * 1000}, true
	}
	if table == nil {
		table = DefaultPriceTable()
	}
	price, lk := table.Lookup(p.ModelName())
	if !lk.Found() {
		return Price{}, false
	}
	return price, true
}
