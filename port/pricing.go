// pricing.go — 模型定价：内建价表 + 归一化查价 + 调用方覆盖。
//
// # 为什么价格要进 llm 层
//
// 此前 `llm` 有完整的成本追踪能力（CostTracker/CostRecord），但**价格不在这一层**：
// `CostTracker.Record` 要求调用方每次传入单价。于是每个下游都得自己维护一张
// 「模型名 → 价格」的表（通常落在应用配置里，键是模型名精确匹配），而：
//
//  1. 漏配 = 成本**静默记 0**——0 成本在账单上与「免费模型」不可区分；
//  2. 路由后缀对不上（`mimo-v2.5-pro-20250101`、部署名前缀）→ 静默漏配；
//  3. 换模型、改价目表要改每个下游的配置。
//
// 本文件把价格收进框架层：内建价表随包维护，价目表变动时升版本即可，下游自动受益。
//
// # ⚠️ 价目表是**参考价**，不是权威报价
//
//   - 采集时间见 builtinPricing 的注释；**厂商随时调价，本表必然滞后于厂商公告**。
//   - 同一模型在不同渠道（官方 / 代理 / 云托管）价格不同，缓存命中、批量、
//     区域定价也另算。本表只反映官方 list price。
//   - 契约价、代理加价、缓存折扣一律用 NewPriceTable 的 overrides 覆盖。
//
// # 单位
//
// **所有单价一律是「美元 / 每 100 万 token」**（Price.InputPerM / OutputPerM）。
// 本仓历史上并存过「每 1K token」（Provider.CostPer1KTokens、CostTracker.Record
// 的 costPer1K 参数）——两套单位并存是**一个数量级的错账**来源，故新 API 全部
// 收敛到每 1M；旧入口保留但标记 Deprecated。
package port

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Price 单价（**美元 / 每 100 万 token**）。
//
// 输入、输出各一档。缓存命中价、分档价不在本结构内——那些是**按厂商规则**算的，
// 硬塞进一个 flat 单价只会算错；需要时由调用方覆盖。
type Price struct {
	InputPerM  float64
	OutputPerM float64
}

// CostOf 按本单价算一次调用的成本（美元）。
//
// 关系式：cost = promptTokens/1e6×InputPerM + completionTokens/1e6×OutputPerM。
func (p Price) CostOf(promptTokens, completionTokens int) float64 {
	return float64(promptTokens)/1e6*p.InputPerM + float64(completionTokens)/1e6*p.OutputPerM
}

// PriceStatus 计价状态。
type PriceStatus string

const (
	// PricePriced 已按单价算出成本。
	PricePriced PriceStatus = "priced"
	// PriceUnpriced **价格未知**：价表里查不到这个模型。
	//
	// 它**不等于「成本为 0」**——CostRecord.CostUSD 此时是 0，但那只是占位，
	// 真实成本未知。两者必须可区分：把「未知」记成 0，是本仓要根除的静默失真。
	PriceUnpriced PriceStatus = "unpriced"
)

// MatchKind 查价命中方式。
type MatchKind string

const (
	// MatchNone 未命中（价格未知）。
	MatchNone MatchKind = "none"
	// MatchExact 原名精确命中。
	MatchExact MatchKind = "exact"
	// MatchNormalized 归一化后命中（去部署前缀 / 去日期后缀）。
	MatchNormalized MatchKind = "normalized"
)

// PriceLookup 查价结果：命中的键 + 命中方式。
//
// 命中方式对外可见，是为了让调用方能审计「这个价格是怎么来的」——
// 归一化命中与精确命中在**同族异价 SKU** 上的后果不同（见 PriceTable.Lookup）。
type PriceLookup struct {
	// Key 命中的价表键（未命中为空串）。
	Key string
	// Kind 命中方式。
	Kind MatchKind
}

// Found 是否查到了价。
func (l PriceLookup) Found() bool { return l.Kind != MatchNone }

// ErrPriceUnknown 价表里查不到该模型。
//
// 用 LookupPriceStrict 拿这个哨兵；errors.Is 可判定。
var ErrPriceUnknown = errors.New("llm: 模型价格未知（价表未收录该模型）")

// builtinPricing 内建价表（美元 / 每 1M token）。
//
// 采集时间：2026-10-20。**这是参考价快照，必然滞后于厂商公告**（见文件头说明）。
//
// 收录原则：只收**厂商以美元公布 list price** 的模型。人民币计价的一律不收
// （通义/百炼的阶梯价、智谱的统一 token 计价等）——换算汇率会漂，阶梯价也无法
// 用单一「每 1M 常数」表达，硬折算等于给出一个**看起来精确的错误数字**。
// 这类模型请用 NewPriceTable 的 overrides 覆盖。
var builtinPricing = map[string]Price{
	// OpenAI（官方 list price）。
	"gpt-4o":       {InputPerM: 2.50, OutputPerM: 10.00},
	"gpt-4o-mini":  {InputPerM: 0.15, OutputPerM: 0.60},
	"gpt-4.1":      {InputPerM: 2.00, OutputPerM: 8.00},
	"gpt-4.1-mini": {InputPerM: 0.40, OutputPerM: 1.60},
	"gpt-4.1-nano": {InputPerM: 0.10, OutputPerM: 0.40},
	"gpt-5-mini":   {InputPerM: 0.30, OutputPerM: 1.00},
	"gpt-5.1":      {InputPerM: 1.25, OutputPerM: 10.00},
	"o3-mini":      {InputPerM: 1.10, OutputPerM: 4.40},

	// Anthropic（官方 list price）。
	"claude-sonnet-4.6": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-haiku-4.5":  {InputPerM: 1.00, OutputPerM: 5.00},
	"claude-3.5-haiku":  {InputPerM: 0.80, OutputPerM: 4.00},

	// DeepSeek（官方 list price）。
	"deepseek-chat":     {InputPerM: 0.28, OutputPerM: 0.42},
	"deepseek-reasoner": {InputPerM: 0.28, OutputPerM: 0.42},
	"deepseek-v4-flash": {InputPerM: 0.14, OutputPerM: 0.28},
	"deepseek-v4-pro":   {InputPerM: 0.435, OutputPerM: 0.87},

	// 小米 MiMo（官方 list price，2026-05-27 永久降价后）。
	"mimo-v2.5-pro":  {InputPerM: 1.00, OutputPerM: 3.00},
	"mimo-v2.5-omni": {InputPerM: 1.50, OutputPerM: 4.00},
	"mimo-v2-flash":  {InputPerM: 0.50, OutputPerM: 1.50},
}

// PriceTable 价表：内建价 + 调用方覆盖。**构造后只读**，可并发使用。
type PriceTable struct {
	table map[string]Price
}

// defaultPriceTable 内建表只构造一次（sync.OnceValue：Go 1.21+）。
var defaultPriceTable = sync.OnceValue(func() *PriceTable { return newPriceTable(nil) })

// DefaultPriceTable 返回内建价表（无覆盖）。
//
// 返回的是**共享只读实例**，不要修改它；要改价请用 NewPriceTable 或
// WithOverride 造一份新的。
func DefaultPriceTable() *PriceTable { return defaultPriceTable() }

// NewPriceTable 造一份价表：内建价 + overrides 覆盖。
//
// overrides 的键走与内建表**同一套归一化规则**（见 normalizeCandidates），
// 所以覆盖 `mimo-v2.5-pro` 也能盖住 `mimo-v2.5-pro-20250101` 这类变体。
// 覆盖优先于内建价。
func NewPriceTable(overrides map[string]Price) *PriceTable {
	return newPriceTable(overrides)
}

func newPriceTable(overrides map[string]Price) *PriceTable {
	t := &PriceTable{table: make(map[string]Price, len(builtinPricing)+len(overrides))}
	for k, v := range builtinPricing {
		t.table[k] = v
	}
	for k, v := range overrides {
		t.table[k] = v
	}
	return t
}

// WithOverride 返回一份新价表：当前表 + 该覆盖（不动原表，原表可能正被并发读）。
func (t *PriceTable) WithOverride(model string, price Price) *PriceTable {
	cp := &PriceTable{table: make(map[string]Price, len(t.table)+1)}
	for k, v := range t.table {
		cp.table[k] = v
	}
	cp.table[model] = price
	return cp
}

// Models 返回表里的全部模型名（字典序）。
func (t *PriceTable) Models() []string {
	out := make([]string, 0, len(t.table))
	for k := range t.table {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Lookup 查价。
//
// # 归一化规则（按优先级）
//
//  1. 原名精确命中（MatchExact）；
//  2. 去掉**部署前缀**（取最后一个 '/' 之后）后精确命中，如 `openai/gpt-4o` → `gpt-4o`；
//  3. 去掉**日期后缀**后精确命中，如 `mimo-v2.5-pro-20250101` → `mimo-v2.5-pro`、
//     `gpt-4o-2024-08-06` → `gpt-4o`；
//  4. 上面组合都试过仍不中 → **未命中**（MatchNone，价格未知）。
//
// # 为什么不做「最长前缀回落」
//
// 旧版 PriceOf/PriceOfMatched 会做最长前缀回落，那会把**同主干异价 SKU** 静默
// 定价错：表里有 `gpt-4o` 时，`gpt-4o-mini`（便宜一半）会按 gpt-4o 计费。
// 「静默算错」比「显式未知」更危险——所以新查价不做前缀回落；需要的模型请显式
// 收录进表里（旧 API 仍保留该行为，见 PriceOf 的 Deprecated 说明）。
func (t *PriceTable) Lookup(model string) (Price, PriceLookup) {
	for _, cand := range normalizeCandidates(model) {
		if p, ok := t.table[cand]; ok {
			kind := MatchExact
			if cand != model {
				kind = MatchNormalized
			}
			return p, PriceLookup{Key: cand, Kind: kind}
		}
	}
	return Price{}, PriceLookup{Kind: MatchNone}
}

// LookupPrice 用内建价表查价。
func LookupPrice(model string) (Price, PriceLookup) {
	return DefaultPriceTable().Lookup(model)
}

// LookupPriceStrict 查价，查不到返回 ErrPriceUnknown。
//
// 给「必须硬失败」的路径用（例如按量计费的出账链路：宁可报错也不能按 0 记账）。
// 非硬失败路径用 LookupPrice / CostTracker.RecordAuto——那里会**标记未定价**而不是丢弃用量。
func LookupPriceStrict(model string) (Price, error) {
	p, lk := LookupPrice(model)
	if !lk.Found() {
		return Price{}, ErrPriceUnknown
	}
	return p, nil
}

// 日期后缀：-20250101 / -2025-01-01 / -202501。
var dateSuffixRe = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$|-\d{8}$|-\d{6}$`)

// normalizeCandidates 生成查价候选序列（顺序即优先级）。
//
// 规则（与 PriceTable.Lookup 的文档一一对应）：
//
//	C0 = 原名
//	C1 = 原名去掉部署前缀（最后一个 '/' 之后）
//	C2 = C1 去掉日期后缀
//	C3 = 原名去掉日期后缀
func normalizeCandidates(model string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(model)
	base := model
	if i := strings.LastIndex(model, "/"); i >= 0 {
		base = model[i+1:]
	}
	add(base)
	add(dateSuffixRe.ReplaceAllString(base, ""))
	add(dateSuffixRe.ReplaceAllString(model, ""))
	return out
}

// ---------------------------------------------------------------- 旧 API

// PriceOf 按模型名匹配定价表估算单次调用成本（USD；未配价返回 0，不阻塞记账）。
//
// Deprecated: 未配价时返回 0，与「免费模型」不可区分——这正是本仓要根除的静默失真。
// 改用 PriceTable.Lookup / LookupPrice：查不到时是「价格未知」而非 0。
//
// 另注意其「最长前缀回落」会把同主干异价 SKU 静默定价错（`gpt-4o-mini` 命中
// `gpt-4o`），新查价 API 不再提供该行为。
func PriceOf(pricing map[string]Price, model string, prompt, completion int) float64 {
	p, ok := pricing[model]
	if !ok {
		bestLen := 0
		for prefix, cand := range pricing {
			if len(prefix) > bestLen && strings.HasPrefix(model, prefix) {
				p, bestLen = cand, len(prefix)
			}
		}
		if bestLen == 0 {
			return 0
		}
	}
	return (float64(prompt)*p.InputPerM + float64(completion)*p.OutputPerM) / 1e6
}

// PriceOfMatched PriceOf 的可观测形态：返回命中的定价键与是否经前缀回落。
//
// 前缀回落对「同主干异价 SKU」（gpt-4o 前缀命中 gpt-4o-mini）会静默误定价
// ——宿主可在装配期对回落命中做校验/上报（第十轮审计）。
//
// Deprecated: 同 PriceOf——未配价返回 0 不可区分「免费」，前缀回落会误定价。
// 改用 PriceTable.Lookup（返回 PriceLookup，命中方式可见且不做前缀回落）。
func PriceOfMatched(pricing map[string]Price, model string) (Price, string, bool) {
	if p, ok := pricing[model]; ok {
		return p, model, false
	}
	best, bestLen := Price{}, -1
	for prefix, cand := range pricing {
		if len(prefix) > bestLen && strings.HasPrefix(model, prefix) {
			best, bestLen = cand, len(prefix)
		}
	}
	if bestLen < 0 {
		return Price{}, "", false
	}
	return best, model[:bestLen], true // 命中键=定价表中的前缀
}
