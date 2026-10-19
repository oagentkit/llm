package llm

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/cloudwego/eino/components/model"

	"github.com/oagentkit/llm/llmtest"
)

// ⚠️ 本文件的断言一律用**关系**（按价表查到的单价 × token 数 == 记录的成本），
// 不写死价目表里的具体数值——价目表随厂商调价变动，写死数字的用例会随价表
// 更新而假失败。只有「表里应该有哪些模型键」这类**契约**才写死键名。

const eps = 1e-9

// ---------- 价表查询 ----------

// TestPriceTableLookupRoundTrip 内建价表每个键都要能被原名精确查到，
// 且「查到的价 × token 数 == 算出的成本」这条关系成立。
func TestPriceTableLookupRoundTrip(t *testing.T) {
	table := DefaultPriceTable()
	models := table.Models()
	if len(models) == 0 {
		t.Fatal("内建价表为空")
	}
	for _, m := range models {
		price, lk := table.Lookup(m)
		if !lk.Found() {
			t.Fatalf("内建价表里的 %q 应能原名查到", m)
		}
		if lk.Key != m {
			t.Fatalf("%q 命中的键应是它自己，实际 %q", m, lk.Key)
		}
		if lk.Kind != MatchExact {
			t.Fatalf("%q 应是精确命中，实际 %v", m, lk.Kind)
		}
		// 关系：1M 输入 + 1M 输出的成本 == 两个单价之和
		got := price.CostOf(1_000_000, 1_000_000)
		want := price.InputPerM + price.OutputPerM
		if math.Abs(got-want) > eps {
			t.Fatalf("%q 成本关系不成立: got=%v want=%v", m, got, want)
		}
	}
}

// TestPriceTableNormalization 归一化规则（文档见 PriceTable.Lookup）。
//
// 用**本地价表**测规则本身，不依赖内建表的内容——规则变了这里必红，
// 而内建表增删模型不会误伤这个用例。
func TestPriceTableNormalization(t *testing.T) {
	tbl := NewPriceTable(map[string]Price{
		"mimo-v2.5-pro": {InputPerM: 1, OutputPerM: 3},
		"gpt-4o":        {InputPerM: 2.5, OutputPerM: 10},
	})
	cases := []struct {
		name     string
		model    string
		wantKey  string
		wantKind MatchKind
	}{
		{"原名精确命中", "mimo-v2.5-pro", "mimo-v2.5-pro", MatchExact},
		{"日期后缀 YYYYMMDD", "mimo-v2.5-pro-20250101", "mimo-v2.5-pro", MatchNormalized},
		{"日期后缀 YYYY-MM-DD", "gpt-4o-2024-08-06", "gpt-4o", MatchNormalized},
		{"部署前缀", "openai/gpt-4o", "gpt-4o", MatchNormalized},
		{"部署前缀+日期后缀", "azure/gpt-4o-2024-08-06", "gpt-4o", MatchNormalized},
		{"完全未知", "totally-unknown-model-xyz", "", MatchNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, lk := tbl.Lookup(c.model)
			if lk.Key != c.wantKey || lk.Kind != c.wantKind {
				t.Fatalf("Lookup(%q) = key %q kind %v，期望 key %q kind %v",
					c.model, lk.Key, lk.Kind, c.wantKey, c.wantKind)
			}
		})
	}
}

// TestBuiltinPricingAliasResolves 带日期后缀的模型名要能查到主干的价，
// 且**价格与直接查主干完全一致**（关系断言，不写死数值）。
func TestBuiltinPricingAliasResolves(t *testing.T) {
	alias, lk := LookupPrice("mimo-v2.5-pro-20250101")
	if !lk.Found() {
		t.Fatal("带日期后缀的模型名应能查到价")
	}
	if lk.Key != "mimo-v2.5-pro" {
		t.Fatalf("应命中主干 mimo-v2.5-pro，实际 %q", lk.Key)
	}
	direct, dlk := LookupPrice("mimo-v2.5-pro")
	if !dlk.Found() {
		t.Fatal("主干模型名应能查到价")
	}
	if alias != direct {
		t.Fatalf("别名价与主干价应一致: alias=%v direct=%v", alias, direct)
	}
}

// TestPriceTableOverrideWins 覆盖价优先于内建价，且不污染内建表。
func TestPriceTableOverrideWins(t *testing.T) {
	const model = "gpt-4o"
	if _, lk := DefaultPriceTable().Lookup(model); !lk.Found() {
		t.Skipf("内建价表当前没有 %q，跳过覆盖优先级用例", model)
	}
	override := Price{InputPerM: 1.25, OutputPerM: 3.5}
	tbl := NewPriceTable(map[string]Price{model: override})
	got, lk := tbl.Lookup(model)
	if !lk.Found() || lk.Key != model {
		t.Fatalf("覆盖表的键应是 %q，实际 %q(found=%v)", model, lk.Key, lk.Found())
	}
	if got != override {
		t.Fatalf("覆盖价应优先于内建价: got=%v want=%v", got, override)
	}
	// 内建表不受影响
	base, _ := DefaultPriceTable().Lookup(model)
	if base == override {
		t.Fatal("覆盖不应污染内建价表")
	}
	// WithOverride 造新表，原表不动
	derived := DefaultPriceTable().WithOverride(model, override)
	dgot, _ := derived.Lookup(model)
	if dgot != override {
		t.Fatalf("WithOverride 后应取到覆盖价: %v", dgot)
	}
	if base, _ := DefaultPriceTable().Lookup(model); base == override {
		t.Fatal("WithOverride 不得改动原表")
	}
}

// TestLookupPriceStrict 硬失败路径：查不到即 ErrPriceUnknown。
func TestLookupPriceStrict(t *testing.T) {
	if _, err := LookupPriceStrict("no-such-model-xyz-42"); !errors.Is(err, ErrPriceUnknown) {
		t.Fatalf("未知模型应返回 ErrPriceUnknown，实际 %v", err)
	}
	table := DefaultPriceTable()
	models := table.Models()
	if len(models) == 0 {
		t.Skip("内建价表为空")
	}
	if _, err := LookupPriceStrict(models[0]); err != nil {
		t.Fatalf("已知模型不应报错: %v", err)
	}
}

// ---------- 成本追踪：未知价不记 0 ----------

// TestCostTrackerRecordAutoKnown 已知模型：成本 == 查表单价 × token 数。
func TestCostTrackerRecordAutoKnown(t *testing.T) {
	table := DefaultPriceTable()
	models := table.Models()
	if len(models) == 0 {
		t.Skip("内建价表为空")
	}
	model := models[0]
	price, lk := table.Lookup(model)
	if !lk.Found() {
		t.Fatalf("%q 应能查到价", model)
	}
	ct := NewCostTrackerWithTable(table)
	ct.RecordAuto(model, "R1", 1000, 2000)

	recs := ct.Records()
	if len(recs) != 1 {
		t.Fatalf("应有 1 条记录，实际 %d", len(recs))
	}
	r := recs[0]
	if r.PriceStatus != PricePriced {
		t.Fatalf("已知模型应是 priced，实际 %v", r.PriceStatus)
	}
	if r.PriceKey != lk.Key {
		t.Fatalf("记录应带上命中的价表键 %q，实际 %q", lk.Key, r.PriceKey)
	}
	want := price.CostOf(1000, 2000)
	if math.Abs(r.CostUSD-want) > eps {
		t.Fatalf("成本关系不成立: got=%v want=%v（price=%v）", r.CostUSD, want, price)
	}
	if ct.UnpricedCount() != 0 {
		t.Fatalf("已知模型不该进未定价清单: %d", ct.UnpricedCount())
	}
}

// TestCostTrackerRecordAutoUnknownNotZero **核心用例**：未知模型绝不能被记成
// 「成本 0 的已计价记录」——它必须被标记为未定价，且可查询到是哪条。
func TestCostTrackerRecordAutoUnknownNotZero(t *testing.T) {
	const unknown = "no-such-model-xyz-42"
	ct := NewCostTracker()
	ct.RecordAuto(unknown, "R1", 1000, 2000)

	recs := ct.Records()
	if len(recs) != 1 {
		t.Fatalf("应有 1 条记录，实际 %d", len(recs))
	}
	r := recs[0]
	if r.PriceStatus != PriceUnpriced {
		t.Fatalf("未知模型必须标记为未定价，实际 %v", r.PriceStatus)
	}
	if r.PriceKey != "" {
		t.Fatalf("未定价记录不应有命中的价表键，实际 %q", r.PriceKey)
	}
	// 可查询：能定位到是哪条
	un := ct.Unpriced()
	if len(un) != 1 {
		t.Fatalf("未定价清单应有 1 条，实际 %d", len(un))
	}
	if un[0].Model != unknown || un[0].Stage != "R1" {
		t.Fatalf("未定价清单应能定位到模型与阶段，实际 %+v", un[0])
	}
	if un[0].PromptTokens != 1000 || un[0].CompletionTokens != 2000 {
		t.Fatalf("未定价记录仍应保留用量（不丢数据），实际 %+v", un[0])
	}
	if ct.UnpricedCount() != 1 {
		t.Fatalf("未定价计数应为 1，实际 %d", ct.UnpricedCount())
	}
	// 汇总层同样要标出来
	s, ok := ct.Summary()[unknown]
	if !ok {
		t.Fatal("汇总里应有该模型")
	}
	if s.UnpricedCalls != 1 || s.PricedCalls != 0 {
		t.Fatalf("汇总应记 1 次未定价: %+v", s)
	}
}

// TestCostTrackerDistinguishesUnpricedFromFree 「成本为 0」与「价格未知」
// 必须可区分——否则成本看板上的 0 既可能是免费，也可能是没查到价。
func TestCostTrackerDistinguishesUnpricedFromFree(t *testing.T) {
	table := DefaultPriceTable()
	models := table.Models()
	if len(models) == 0 {
		t.Skip("内建价表为空")
	}
	known := models[0]
	const unknown = "no-such-model-xyz-42"

	ct := NewCostTrackerWithTable(table)
	ct.RecordAuto(known, "R1", 0, 0)   // 零用量 ⇒ 成本 0，但**已定价**
	ct.RecordAuto(unknown, "R1", 0, 0) // 零用量 + 未知模型 ⇒ 成本 0，**未定价**

	sum := ct.Summary()
	ks := sum[known]
	us := sum[unknown]
	if ks.TotalCostUSD != 0 || us.TotalCostUSD != 0 {
		t.Fatalf("两者成本都应为 0（零用量）: known=%v unknown=%v", ks.TotalCostUSD, us.TotalCostUSD)
	}
	if ks.UnpricedCalls != 0 || ks.PricedCalls != 1 {
		t.Fatalf("已定价的零成本调用不应进未定价清单: %+v", ks)
	}
	if us.UnpricedCalls != 1 || us.PricedCalls != 0 {
		t.Fatalf("未知模型必须落在未定价计数上: %+v", us)
	}
}

// TestCostTrackerRecordPriced 调用方显式给价（折扣/加价）时按给的价算。
func TestCostTrackerRecordPriced(t *testing.T) {
	price := Price{InputPerM: 2, OutputPerM: 4}
	ct := NewCostTracker()
	ct.RecordPriced("some-private-model", "R1", 500, 250, price)
	r := ct.Records()[0]
	if r.PriceStatus != PricePriced {
		t.Fatalf("显式给价应是 priced，实际 %v", r.PriceStatus)
	}
	want := price.CostOf(500, 250)
	if math.Abs(r.CostUSD-want) > eps {
		t.Fatalf("显式价成本关系不成立: got=%v want=%v", r.CostUSD, want)
	}
	if ct.UnpricedCount() != 0 {
		t.Fatalf("显式给价不该算未定价: %d", ct.UnpricedCount())
	}
}

// ---------- Provider 侧单价解析 ----------

// priceProviderStub 最小 Provider 桩（只关心价格解析路径）。
type priceProviderStub struct {
	name   string
	cost1K [2]float64
}

func (p priceProviderStub) Name() string                        { return "stub" }
func (p priceProviderStub) Model() model.BaseChatModel          { return nil }
func (p priceProviderStub) ModelName() string                   { return p.name }
func (p priceProviderStub) ContextTokens() int                  { return 0 }
func (p priceProviderStub) MaxOutputTokens() int                { return 0 }
func (p priceProviderStub) CostPer1KTokens() (float64, float64) { return p.cost1K[0], p.cost1K[1] }

// pricedProviderStub 额外实现 PricedProvider（显式单价）。
type pricedProviderStub struct {
	priceProviderStub
	price Price
	ok    bool
}

func (p pricedProviderStub) ModelPricing() (Price, bool) { return p.price, p.ok }

// TestProviderPricePriority 单价解析优先级：
// PricedProvider 显式价 > CostPer1KTokens 非零 > 价表按模型名 > 未知。
func TestProviderPricePriority(t *testing.T) {
	explicit := Price{InputPerM: 7, OutputPerM: 9}
	per1K := [2]float64{0.001, 0.002}

	// 1) 显式 PricedProvider 压过 per-1K 与价表
	p := pricedProviderStub{
		priceProviderStub: priceProviderStub{name: "gpt-4o", cost1K: per1K},
		price:             explicit, ok: true,
	}
	got, ok := providerPrice(p, DefaultPriceTable())
	if !ok || got != explicit {
		t.Fatalf("显式价应优先: got=%v ok=%v", got, ok)
	}

	// 2) per-1K 压过价表（并换算到每 1M）
	p2 := priceProviderStub{name: "gpt-4o", cost1K: per1K}
	got, ok = providerPrice(p2, DefaultPriceTable())
	if !ok {
		t.Fatal("per-1K 非零应算已定价")
	}
	if math.Abs(got.InputPerM-per1K[0]*1000) > eps || math.Abs(got.OutputPerM-per1K[1]*1000) > eps {
		t.Fatalf("per-1K→per-1M 换算不对: got=%v", got)
	}

	// 3) 都没有 → 回落到价表
	p3 := priceProviderStub{name: "gpt-4o"}
	got, ok = providerPrice(p3, DefaultPriceTable())
	if !ok {
		t.Fatal("应回落到内建价表")
	}
	ref, lk := LookupPrice("gpt-4o")
	if !lk.Found() || got != ref {
		t.Fatalf("回落价应等于内建价表: got=%v ref=%v", got, ref)
	}

	// 4) 都没有且查不到 ⇒ 未知（**不是 0 价**）
	p4 := priceProviderStub{name: "no-such-model-xyz-42"}
	if got, ok = providerPrice(p4, DefaultPriceTable()); ok {
		t.Fatalf("查不到应返回未知，实际 got=%v", got)
	}
}

// namedProvider 覆盖 ModelName 的 llmtest.Provider 桩（llmtest 的 ModelName
// 固定是 Label+"-model"，这里要精确控制模型名才能命中价表）。
type namedProvider struct {
	*llmtest.Provider
	name string
}

func (p namedProvider) ModelName() string { return p.name }

// TestResilientTrackerPricing 生产路径（Resilient → Client.OnUsage → Tracker）：
//
//   - Provider 显式给价 → 按给的价记；
//   - Provider 没给价但模型在价表里 → 按**内建价表**记（此前这里恒记 0）；
//   - Provider 没给价、模型也不在价表里 → 记为**未定价**，不是 0 成本。
func TestResilientTrackerPricing(t *testing.T) {
	table := DefaultPriceTable()
	models := table.Models()
	if len(models) == 0 {
		t.Skip("内建价表为空")
	}
	known := models[0]
	const unknown = "no-such-model-xyz-42"

	newProv := func(name string) namedProvider {
		p := llmtest.NewScriptedProvider("stub", llmtest.Resp{
			Content: "ok", Prompt: 100, Completion: 50,
		})
		p.Costs = [2]float64{} // 不显式给价 → 逼出价表回落路径
		return namedProvider{Provider: p, name: name}
	}

	// 1) 已知模型：按内建价表计价（关系断言）
	tracker := NewCostTrackerWithTable(table)
	prov := newProv(known)
	r, err := NewResilient(NewFallbackChain(prov), ResilientConfig{
		RetriesPerModel: 0, BaseDelay: 1, MaxDelay: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Tracker = tracker
	if _, err := r.Generate(context.Background(), "R1", msgs("q")); err != nil {
		t.Fatal(err)
	}
	recs := tracker.Records()
	if len(recs) != 1 {
		t.Fatalf("应有 1 条记录，实际 %d", len(recs))
	}
	if recs[0].PriceStatus != PricePriced {
		t.Fatalf("已知模型应按内建价表计价，实际 %v", recs[0].PriceStatus)
	}
	price, lk := table.Lookup(known)
	if !lk.Found() {
		t.Fatalf("%q 应在价表里", known)
	}
	want := price.CostOf(100, 50)
	if math.Abs(recs[0].CostUSD-want) > eps {
		t.Fatalf("成本关系不成立: got=%v want=%v", recs[0].CostUSD, want)
	}
	if tracker.UnpricedCount() != 0 {
		t.Fatalf("已知模型不该进未定价清单: %d", tracker.UnpricedCount())
	}

	// 2) 未知模型：记未定价，**不记 0 成本的正常记录**
	tracker2 := NewCostTracker()
	r2, err := NewResilient(NewFallbackChain(newProv(unknown)), ResilientConfig{
		RetriesPerModel: 0, BaseDelay: 1, MaxDelay: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	r2.Tracker = tracker2
	if _, err := r2.Generate(context.Background(), "R1", msgs("q")); err != nil {
		t.Fatal(err)
	}
	recs2 := tracker2.Records()
	if len(recs2) != 1 {
		t.Fatalf("应有 1 条记录，实际 %d", len(recs2))
	}
	if recs2[0].PriceStatus != PriceUnpriced {
		t.Fatalf("未知模型必须记为未定价，实际 %v", recs2[0].PriceStatus)
	}
	if tracker2.UnpricedCount() != 1 {
		t.Fatalf("未定价计数应为 1，实际 %d", tracker2.UnpricedCount())
	}
	if s := tracker2.Summary()[unknown]; s.UnpricedCalls != 1 {
		t.Fatalf("汇总应记 1 次未定价: %+v", s)
	}
}
