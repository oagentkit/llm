package obsx

import "testing"

// TestObservationUsageFallback 钉 usage/cost 的**双口径**契约：
// 新口径（usageDetails/costDetails）优先，旧口径（usage）兜底——
// 两者都在同一份观测载荷里出现过，只认新口径会让旧数据静默读成 0。
func TestObservationUsageFallback(t *testing.T) {
	// 新口径优先
	o := Observation{
		UsageDetails: map[string]int{"input": 3, "output": 4, "total": 7},
		CostDetails:  map[string]float64{"total": 0.25},
	}
	o.Usage.Input, o.Usage.Output, o.Usage.Total = 99, 98, 197
	o.Usage.TotalCost = 9.99
	in, out, total := o.UsageTokens()
	if in != 3 || out != 4 || total != 7 {
		t.Fatalf("新口径 usageDetails 优先: %d/%d/%d", in, out, total)
	}
	if c := o.UsageCost(); c != 0.25 {
		t.Fatalf("新口径 costDetails 优先: %v", c)
	}

	// 旧口径兜底（新口径缺席）
	o2 := Observation{}
	o2.Usage.Input, o2.Usage.Output, o2.Usage.Total = 1, 2, 3
	o2.Usage.TotalCost = 1.5
	in, out, total = o2.UsageTokens()
	if in != 1 || out != 2 || total != 3 {
		t.Fatalf("旧口径 usage 兜底: %d/%d/%d", in, out, total)
	}
	if c := o2.UsageCost(); c != 1.5 {
		t.Fatalf("旧口径 usage.totalCost 兜底: %v", c)
	}
}
