package port

import "testing"

// PriceOf：精确匹配优先、最长前缀回落、未配价归零（bianque 生产语义）。
func TestPriceOf(t *testing.T) {
	pricing := map[string]Price{
		"deepseek-chat": {InputPerM: 0.27, OutputPerM: 1.1},
		"deepseek":      {InputPerM: 1.0, OutputPerM: 2.0},
		"qwen-max":      {InputPerM: 1.6, OutputPerM: 6.4},
	}
	cost := PriceOf(pricing, "deepseek-chat", 1_000_000, 1_000_000)
	if cost != 1.37 {
		t.Fatalf("精确匹配: cost=%v want 1.37", cost)
	}
	cost = PriceOf(pricing, "deepseek-chat-20260901", 1_000_000, 0)
	if cost != 0.27 {
		t.Fatalf("最长前缀回落应命中 deepseek-chat: %v", cost)
	}
	if PriceOf(pricing, "unknown-model", 1_000_000, 1_000_000) != 0 {
		t.Fatal("未配价应返回 0")
	}
	if PriceOf(nil, "deepseek-chat", 1, 1) != 0 {
		t.Fatal("空定价表应返回 0")
	}
}

// CachedTokens：观测入库不改计价口径（缓存折扣在 Price 层由调用方覆盖），
// 命中率从 Summary 派生；clampCached 钳 [0,prompt] 防坏数据污染命中率。
func TestCostTrackerCachedTokens(t *testing.T) {
	ct := NewCostTracker()
	price := Price{InputPerM: 1.0, OutputPerM: 2.0}
	// 一半命中：cached 计价仍是全价 prompt（缓存折扣不在本层）——成本口径不变。
	ct.RecordPricedCached("m", "R3", 1000, 400, 0, price)
	recs := ct.Records()
	if recs[0].CachedTokens != 400 {
		t.Fatalf("cached 应入库: got %d want 400", recs[0].CachedTokens)
	}
	if recs[0].CostUSD != price.CostOf(1000, 0) {
		t.Fatalf("cached 不应改计价: got %v want %v", recs[0].CostUSD, price.CostOf(1000, 0))
	}
	// 坏数据：cached 超 prompt 钳回 prompt，负值钳 0。
	ct.RecordPricedCached("m", "", 100, 500, 0, price)
	ct.RecordAutoCached("m", "", 100, -5, 0)
	recs = ct.Records()
	if recs[1].CachedTokens != 100 {
		t.Fatalf("cached>prompt 应钳 prompt: got %d", recs[1].CachedTokens)
	}
	if recs[2].CachedTokens != 0 {
		t.Fatalf("cached<0 应钳 0: got %d", recs[2].CachedTokens)
	}
	// 命中率 = Σcached / Σprompt = (400+100+0)/(1000+100+100) = 500/1200。
	s := ct.Summary()["m"]
	wantRatio := 500.0 / 1200.0
	if r := s.CacheHitRatio(); r < wantRatio-1e-9 || r > wantRatio+1e-9 {
		t.Fatalf("命中率: got %v want %v", r, wantRatio)
	}
	if s.TotalCachedTokens != 500 {
		t.Fatalf("Σcached: got %d want 500", s.TotalCachedTokens)
	}
}

// RecordUnpricedCached：价格未知不影响 cached 观测入库。
func TestCostTrackerUnpricedCached(t *testing.T) {
	ct := NewCostTracker()
	ct.RecordUnpricedCached("m", "", 1000, 300, 0)
	rec := ct.Records()[0]
	if rec.CachedTokens != 300 || rec.PriceStatus != PriceUnpriced {
		t.Fatalf("价格未知仍应记 cached: cached=%d status=%v", rec.CachedTokens, rec.PriceStatus)
	}
}
