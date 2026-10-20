package llm

import (
	"context"
	"math"
	"testing"

	"github.com/cloudwego/eino/components/model"

	"github.com/oagentkit/llm/llmtest"
)

// ⚠️ 本文件的断言一律用**关系**（按价表查到的单价 × token 数 == 记录的成本），
// 不写死价目表里的具体数值——价目表随厂商调价变动，写死数字的用例会随价表
// 更新而假失败。只有「表里应该有哪些模型键」这类**契约**才写死键名。

const eps = 1e-9

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

// TestNewPriceTable_OverrideKeyNormalized_IssueRepro 复现 issue #26 的那段程序
// （走公共 API 的类型别名路径）：覆盖键带日期后缀时，**不带形态**的查询必须
// 拿到覆盖价 {9,9}，而不是内建参考价。
func TestNewPriceTable_OverrideKeyNormalized_IssueRepro(t *testing.T) {
	override := Price{InputPerM: 9.0, OutputPerM: 9.0}
	rev := NewPriceTable(map[string]Price{
		"mimo-v2.5-pro-20250101": override,
	})

	// 第一行：同形态查询 → 覆盖价
	p, lk := rev.Lookup("mimo-v2.5-pro-20250101")
	if !lk.Found() || p != override {
		t.Fatalf("同形态查询应是覆盖价: price=%v lookup=%+v", p, lk)
	}

	// 第二行（**修复点**）：不带形态的查询 → 也应是覆盖价 {9,9}
	p, lk = rev.Lookup("mimo-v2.5-pro")
	if !lk.Found() {
		t.Fatal("不带形态的查询应能查到价")
	}
	if p != override {
		t.Fatalf("不带形态的查询应是覆盖价 %v 而不是内建参考价，实际 %v", override, p)
	}
	// 内建参考价确实与之不同（否则用例失去意义）
	if builtin, _ := DefaultPriceTable().Lookup("mimo-v2.5-pro"); builtin == override {
		t.Fatal("内建参考价与覆盖价相同，本用例失去意义")
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
