package port

import (
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/oekit/concurrency/async"
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

// ---------- 覆盖键归一化（issue #26） ----------

// TestPriceTable_OverrideKeyNormalized_CoversBaseForm 覆盖键带**日期后缀**时，
// 不带形态的查询也必须拿到**覆盖价**。
//
// 修复前：NewPriceTable 把覆盖键原样入库，只有同形态查询能命中；不带形态的
// 查询会静默落到内建参考价（found=true / kind=exact，**没有任何信号**提示
// 「这个价不是你配的」）。
func TestPriceTable_OverrideKeyNormalized_CoversBaseForm(t *testing.T) {
	const model = "mimo-v2.5-pro-20250101"
	const base = "mimo-v2.5-pro"
	override := Price{InputPerM: 9, OutputPerM: 9}

	// 前置：内建参考价必须与覆盖价不同，否则本用例测不出东西
	if builtin, _ := DefaultPriceTable().Lookup(base); builtin == override {
		t.Fatal("内建参考价与覆盖价相同，本用例失去意义")
	}

	tbl := NewPriceTable(map[string]Price{model: override})

	// 1) 同形态查询 → 覆盖价
	got, lk := tbl.Lookup(model)
	if !lk.Found() {
		t.Fatalf("Lookup(%q) 应查到价", model)
	}
	if got != override {
		t.Fatalf("Lookup(%q) 应是覆盖价: got=%v want=%v", model, got, override)
	}
	if lk.Kind != MatchExact || lk.Key != model {
		t.Fatalf("Lookup(%q) 应是精确命中且 Key=%q，实际 kind=%v key=%q",
			model, model, lk.Kind, lk.Key)
	}

	// 2) 不带形态的查询 → **也**是覆盖价（修复前这里是内建参考价）
	got, lk = tbl.Lookup(base)
	if !lk.Found() {
		t.Fatalf("Lookup(%q) 应查到价", base)
	}
	if got != override {
		t.Fatalf("Lookup(%q) 应是覆盖价而不是内建参考价: got=%v want=%v", base, got, override)
	}
	if lk.Kind != MatchNormalized {
		t.Fatalf("Lookup(%q) 应是归一化命中，实际 %v", base, lk.Kind)
	}
	// Key 报出**调用方配的键**——「只认自己配的价」的消费者据此回查自己的表
	if lk.Key != model {
		t.Fatalf("Lookup(%q) 的 Key 应是调用方配的键 %q，实际 %q", base, model, lk.Key)
	}
}

// TestPriceTable_OverrideKeyWithDeployPrefix_CoversBaseForm 覆盖键带**部署前缀**时，
// 不带前缀的查询同样必须拿到覆盖价。
func TestPriceTable_OverrideKeyWithDeployPrefix_CoversBaseForm(t *testing.T) {
	const model = "openai/gpt-4o"
	const base = "gpt-4o"
	override := Price{InputPerM: 1, OutputPerM: 2}

	if builtin, _ := DefaultPriceTable().Lookup(base); builtin == override {
		t.Fatal("内建参考价与覆盖价相同，本用例失去意义")
	}

	tbl := NewPriceTable(map[string]Price{model: override})

	got, lk := tbl.Lookup(model)
	if !lk.Found() || got != override {
		t.Fatalf("Lookup(%q) 应是覆盖价: got=%v want=%v found=%v", model, got, override, lk.Found())
	}
	if lk.Kind != MatchExact || lk.Key != model {
		t.Fatalf("Lookup(%q) 应是精确命中且 Key=%q，实际 kind=%v key=%q",
			model, model, lk.Kind, lk.Key)
	}

	got, lk = tbl.Lookup(base)
	if !lk.Found() || got != override {
		t.Fatalf("Lookup(%q) 应是覆盖价而不是内建参考价: got=%v want=%v found=%v",
			base, got, override, lk.Found())
	}
	if lk.Kind != MatchNormalized {
		t.Fatalf("Lookup(%q) 应是归一化命中，实际 %v", base, lk.Kind)
	}
	if lk.Key != model {
		t.Fatalf("Lookup(%q) 的 Key 应是调用方配的键 %q，实际 %q", base, model, lk.Key)
	}
}

// TestPriceTable_OverrideBaseForm_StillCoversDatedVariant 覆盖键是**基础形态**时，
// 带形态的查询仍要命中它——这是文档原本就承诺的**正向**一例，不许回归。
func TestPriceTable_OverrideBaseForm_StillCoversDatedVariant(t *testing.T) {
	const base = "mimo-v2.5-pro"
	const dated = "mimo-v2.5-pro-20250101"
	override := Price{InputPerM: 9, OutputPerM: 9}

	tbl := NewPriceTable(map[string]Price{base: override})

	got, lk := tbl.Lookup(base)
	if !lk.Found() || got != override {
		t.Fatalf("Lookup(%q) 应是覆盖价: got=%v want=%v", base, got, override)
	}
	if lk.Kind != MatchExact || lk.Key != base {
		t.Fatalf("Lookup(%q) 应是精确命中且 Key=%q，实际 kind=%v key=%q",
			base, base, lk.Kind, lk.Key)
	}

	got, lk = tbl.Lookup(dated)
	if !lk.Found() || got != override {
		t.Fatalf("Lookup(%q) 应是覆盖价: got=%v want=%v", dated, got, override)
	}
	if lk.Kind != MatchNormalized || lk.Key != base {
		t.Fatalf("Lookup(%q) 应是归一化命中且 Key=%q，实际 kind=%v key=%q",
			dated, base, lk.Kind, lk.Key)
	}
}

// TestPriceTable_BuiltinTableUnaffected 内建价表的行为**不许变**：带日期后缀的
// 查法与主干查法拿到同一个价，命中方式分别是归一化 / 精确。
func TestPriceTable_BuiltinTableUnaffected(t *testing.T) {
	tbl := DefaultPriceTable()
	const base = "gpt-4o"
	const dated = "gpt-4o-2024-08-06"

	basePrice, baseLk := tbl.Lookup(base)
	if !baseLk.Found() {
		t.Fatalf("内建表应有 %q", base)
	}
	if baseLk.Kind != MatchExact || baseLk.Key != base {
		t.Fatalf("Lookup(%q) 应是精确命中且 Key=%q，实际 kind=%v key=%q",
			base, base, baseLk.Kind, baseLk.Key)
	}

	datedPrice, datedLk := tbl.Lookup(dated)
	if !datedLk.Found() {
		t.Fatalf("内建表应能查到 %q", dated)
	}
	if datedLk.Kind != MatchNormalized || datedLk.Key != base {
		t.Fatalf("Lookup(%q) 应是归一化命中且 Key=%q，实际 kind=%v key=%q",
			dated, base, datedLk.Kind, datedLk.Key)
	}
	// 关系断言（不写死价目表数值——价目表随厂商调价变动）
	if datedPrice != basePrice {
		t.Fatalf("带后缀查询与主干查询的价应一致: dated=%v base=%v", datedPrice, basePrice)
	}
}

// TestPriceTable_OverrideKeyPriority_Rules 同一基座上的多个覆盖键按**固定的
// 优先级规则**只留一个生效（规则见 NewPriceTable 的 godoc）：
//
//  1. 基础形态的键优先；
//  2. 同为派生形态时更长的键优先；
//  3. 再同则字典序小的键优先。
//
// 反复构造价表（map 的迭代顺序随机）——结果必须恒定，否则同一份配置在不同
// 进程里会算出不同的价。
func TestPriceTable_OverrideKeyPriority_Rules(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]Price
		wantKey   string
	}{
		{
			"基础形态优先于部署前缀形态",
			map[string]Price{"gpt-4o": {1, 2}, "openai/gpt-4o": {3, 4}},
			"gpt-4o",
		},
		{
			"基础形态优先于日期后缀形态",
			map[string]Price{"gpt-4o": {1, 2}, "gpt-4o-2024-08-06": {3, 4}},
			"gpt-4o",
		},
		{
			"同为派生形态时更长的键优先（日期后缀）",
			map[string]Price{"gpt-4o-20250101": {1, 2}, "gpt-4o-2024-08-06": {3, 4}},
			"gpt-4o-2024-08-06",
		},
		{
			"同为派生形态时更长的键优先（部署前缀）",
			map[string]Price{"azure/gpt-4o": {1, 2}, "openai/gpt-4o": {3, 4}},
			"openai/gpt-4o",
		},
		{
			"等长时字典序小的优先",
			map[string]Price{"gpt-4o-20250101": {1, 2}, "gpt-4o-20240101": {3, 4}},
			"gpt-4o-20240101",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				tbl := NewPriceTable(c.overrides)
				_, lk := tbl.Lookup("gpt-4o")
				if !lk.Found() {
					t.Fatal("应查到价")
				}
				if lk.Key != c.wantKey {
					t.Fatalf("生效的键应是 %q，实际 %q", c.wantKey, lk.Key)
				}
			}
		})
	}
}

// TestPriceTable_OverrideKeyPriority_OnePricePerFamily 同族只有一个价：落选的
// 覆盖键**整族不生效**（不是只在自己那条查询上落选）。
func TestPriceTable_OverrideKeyPriority_OnePricePerFamily(t *testing.T) {
	tbl := NewPriceTable(map[string]Price{
		"gpt-4o":        {InputPerM: 1, OutputPerM: 2},
		"openai/gpt-4o": {InputPerM: 3, OutputPerM: 4},
	})
	base, baseLk := tbl.Lookup("gpt-4o")
	pref, prefLk := tbl.Lookup("openai/gpt-4o")
	if base != pref {
		t.Fatalf("同族应只有一个价: base=%v prefixed=%v", base, pref)
	}
	if baseLk.Key != prefLk.Key {
		t.Fatalf("同族查询应归到同一个键: %q vs %q", baseLk.Key, prefLk.Key)
	}
	if base != (Price{InputPerM: 1, OutputPerM: 2}) {
		t.Fatalf("应是基础形态键的价生效: got=%v", base)
	}
}

// TestOverridePriority_IsStrictTotalOrder 优先级比较必须是**全序**：任意两个
// 不同的键都能分出先后，且结论只取决于键本身（与 map 迭代顺序、与进程都无关）。
// 这是「同一基座上谁的覆盖生效」跨进程稳定的前提。
func TestOverridePriority_IsStrictTotalOrder(t *testing.T) {
	keys := []string{
		"gpt-4o", "openai/gpt-4o", "azure/gpt-4o",
		"gpt-4o-2024-08-06", "gpt-4o-20250101",
	}
	for _, a := range keys {
		for _, b := range keys {
			if a == b {
				if got := overridePriority(a, b); got != 0 {
					t.Fatalf("overridePriority(%q,%q) 应为 0，实际 %d", a, b, got)
				}
				continue
			}
			ab, ba := overridePriority(a, b), overridePriority(b, a)
			if ab == 0 {
				t.Fatalf("overridePriority(%q,%q) 不该为 0（分不出先后）", a, b)
			}
			if (ab < 0) != (ba > 0) {
				t.Fatalf("overridePriority 不反对称: (%q,%q)=%d 反向=%d", a, b, ab, ba)
			}
		}
	}
}

// TestPriceTable_WithOverride_UsesSameNormalization WithOverride 必须与
// NewPriceTable 走**同一套**归一化规则：带形态的覆盖键也要盖住基础形态的查询。
func TestPriceTable_WithOverride_UsesSameNormalization(t *testing.T) {
	const model = "mimo-v2.5-pro-20250101"
	const base = "mimo-v2.5-pro"
	override := Price{InputPerM: 9, OutputPerM: 9}

	tbl := DefaultPriceTable().WithOverride(model, override)

	got, lk := tbl.Lookup(base)
	if !lk.Found() || got != override {
		t.Fatalf("WithOverride 后 Lookup(%q) 应是覆盖价: got=%v want=%v found=%v",
			base, got, override, lk.Found())
	}
	if lk.Kind != MatchNormalized || lk.Key != model {
		t.Fatalf("Lookup(%q) 应是归一化命中且 Key=%q，实际 kind=%v key=%q",
			base, model, lk.Kind, lk.Key)
	}
	// 原表不动
	if p, _ := DefaultPriceTable().Lookup(base); p == override {
		t.Fatal("WithOverride 不得改动原表")
	}

	// 基础形态的覆盖键同样盖住带形态的查询
	tbl2 := DefaultPriceTable().WithOverride(base, override)
	got, lk = tbl2.Lookup(model)
	if !lk.Found() || got != override {
		t.Fatalf("WithOverride 后 Lookup(%q) 应是覆盖价: got=%v want=%v", model, got, override)
	}
	if lk.Kind != MatchNormalized || lk.Key != base {
		t.Fatalf("Lookup(%q) 应是归一化命中且 Key=%q，实际 kind=%v key=%q",
			model, base, lk.Kind, lk.Key)
	}
}

// TestPriceTable_OverrideDoesNotShadowSiblingModels 覆盖一个模型族**不得**遮蔽
// 内建表里**其它**模型的价——归一化只去部署前缀与日期后缀，
// `gpt-4o-mini-2024-08-06` 折出来是 `gpt-4o-mini` 而不是 `gpt-4o`。
func TestPriceTable_OverrideDoesNotShadowSiblingModels(t *testing.T) {
	override := Price{InputPerM: 1, OutputPerM: 2}
	tbl := NewPriceTable(map[string]Price{"gpt-4o-2024-08-06": override})

	for _, sibling := range []string{"gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano"} {
		want, wlk := DefaultPriceTable().Lookup(sibling)
		if !wlk.Found() {
			t.Fatalf("内建表应收录 %q", sibling)
		}
		got, lk := tbl.Lookup(sibling)
		if !lk.Found() {
			t.Fatalf("%q 应能查到价", sibling)
		}
		if got != want {
			t.Fatalf("%q 的价不该被别的覆盖遮蔽: got=%v want=%v", sibling, got, want)
		}
	}

	// 被覆盖的那一族确实换成了覆盖价
	got, lk := tbl.Lookup("gpt-4o")
	if !lk.Found() || got != override {
		t.Fatalf("gpt-4o 族应是覆盖价: got=%v", got)
	}
	if lk.Key != "gpt-4o-2024-08-06" {
		t.Fatalf("Key 应报出调用方配的键，实际 %q", lk.Key)
	}
}

// TestBuiltinPricingKeysAreBaseForms 内建价表的每个键都必须是**基座形态**
// （无部署前缀 / 日期后缀）——否则它自己的基座折算会落到另一个键上，
// 覆盖价就能意外遮蔽它。
func TestBuiltinPricingKeysAreBaseForms(t *testing.T) {
	for k := range builtinPricing {
		if base := baseForm(k); base != k {
			t.Fatalf("内建键 %q 的基座形态是 %q，应相同（否则覆盖价会遮蔽它）", k, base)
		}
	}
}

// TestPriceTable_LookupKeyIsIdempotent PriceLookup.Key 是**查回同一份价**用的键
// （PriceLookup 的 godoc 承诺了这条不变量）：再查一次它，必须拿到同一个价。
func TestPriceTable_LookupKeyIsIdempotent(t *testing.T) {
	tbl := NewPriceTable(map[string]Price{
		"mimo-v2.5-pro-20250101": {InputPerM: 9, OutputPerM: 9},
		"openai/gpt-4o":          {InputPerM: 1, OutputPerM: 2},
	})
	for _, model := range []string{
		"mimo-v2.5-pro", "mimo-v2.5-pro-20250101",
		"gpt-4o", "openai/gpt-4o",
	} {
		p, lk := tbl.Lookup(model)
		if !lk.Found() {
			t.Fatalf("Lookup(%q) 应查到价", model)
		}
		p2, lk2 := tbl.Lookup(lk.Key)
		if !lk2.Found() || p2 != p {
			t.Fatalf("Lookup(%q) 的 Key=%q 再查应拿到同一个价: %v vs %v", model, lk.Key, p2, p)
		}
	}
}

// TestPriceTable_LookupConcurrent 价表构造后只读，多 goroutine 并发查价必须
// 无 race（配合 -race 跑）。
func TestPriceTable_LookupConcurrent(t *testing.T) {
	override := Price{InputPerM: 9, OutputPerM: 9}
	tbl := NewPriceTable(map[string]Price{"mimo-v2.5-pro-20250101": override})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if p, lk := tbl.Lookup("mimo-v2.5-pro"); !lk.Found() || p != override {
					t.Errorf("并发查价结果不对: price=%v lookup=%v", p, lk)
					return
				}
			}
		})
	}
	wg.Wait()
}
