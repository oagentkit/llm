package port

// 2026-11-14 issue 清零战回归测试（#186/#191 窄接口侧）：预算逐环节与环内
// 重试前复查、失败面按估算记账。每条用例标题带 issue 号——回归时按号检索。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestChainFailedAttemptAccounting_Issue191 失败的物理调用按估算记 prompt 侧：
// 预算照扣、成本记录 completion=0——失败面消耗不再不可见。
func TestChainFailedAttemptAccounting_Issue191(t *testing.T) {
	b := NewBudget(100000)
	tracker := NewCostTracker()
	first := &fakeCompleter{name: "a", err: errors.New("500 server error")}
	second := &fakeCompleter{name: "b", out: "ok"}
	chain := NewChain(
		WithCompleter("a", first), WithCompleter("b", second),
		WithBudget(b), WithTracker(tracker), WithRetries(0),
	)
	if _, err := chain.Complete(context.Background(), Request{Prompt: "hi", Stage: "R1"}); err != nil {
		t.Fatal(err)
	}
	// a 失败记估算 prompt；b 成功记 prompt + completion（est("ok")）。
	estP, estC := estimateTokens("hi"), estimateTokens("ok")
	if got := b.Used(); got != 2*estP+estC {
		t.Fatalf("预算应含失败面估算：got %d want %d", got, 2*estP+estC)
	}
	recs := tracker.Records()
	if len(recs) != 2 {
		t.Fatalf("失败+成功应各记一笔，实际 %d", len(recs))
	}
	if recs[0].Model != "a" || recs[0].CompletionTokens != 0 || recs[0].PromptTokens != estP {
		t.Fatalf("失败面记录应为 a 的 prompt 侧估算、completion 0: %+v", recs[0])
	}
	if !strings.Contains(recs[0].Stage, "R1") {
		t.Fatalf("失败面记录应带 stage: %+v", recs[0])
	}
}
