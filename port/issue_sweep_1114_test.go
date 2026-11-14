package port

// 2026-11-14 issue 清零战回归测试（#186/#191 窄接口侧）：预算逐环节与环内
// 重试前复查、失败面按估算记账。每条用例标题带 issue 号——回归时按号检索。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// drainCompleter 每次被调即向预算注入消耗（模拟并发任务共烧同一任务级预算）
// 后按脚本失败/成功。
type drainCompleter struct {
	name  string
	b     TokenAccountant
	drain int
	err   error
	out   string
	calls int
}

func (f *drainCompleter) Complete(ctx context.Context, req Request) (string, error) {
	f.calls++
	if f.b != nil && f.drain > 0 {
		f.b.Add(f.drain)
	}
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

// TestChainBudgetRecheckedPerEntry_Issue186 第一环失败且预算被烧穿后，
// 第二环不得再发起——Complete 每环节入口复查预算（对齐 Resilient.stepChain）。
func TestChainBudgetRecheckedPerEntry_Issue186(t *testing.T) {
	b := NewBudget(100)
	first := &drainCompleter{name: "a", b: b, drain: 100, err: errors.New("boom")}
	second := &fakeCompleter{name: "b", out: "不该被调用"}
	chain := NewChain(
		WithCompleter("a", first),
		WithCompleter("b", second),
		WithBudget(b),
		WithRetries(0),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi", Stage: "R1"})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("预算烧穿后应返回 ErrBudgetExhausted: %v", err)
	}
	if second.calls != 0 {
		t.Fatalf("第一环烧穿预算后第二环不得发起，实际调了 %d 次", second.calls)
	}
}

// TestChainBudgetGateBeforeRetry_Issue186 环内重试前复查预算：第一轮物理调用
// 失败并把预算烧穿后，同环节的下一轮重试不得发起。
func TestChainBudgetGateBeforeRetry_Issue186(t *testing.T) {
	b := NewBudget(100)
	first := &drainCompleter{name: "a", b: b, drain: 100, err: errors.New("boom")}
	chain := NewChain(WithCompleter("a", first), WithBudget(b), WithRetries(2))
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi", Stage: "R1"})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("重试间隙预算烧穿应短路: %v", err)
	}
	if first.calls != 1 {
		t.Fatalf("预算烧穿后不得再重试，实际物理调用 %d 次", first.calls)
	}
}

// TestChainBudgetSufficientReachesAllEntries_Issue186 正向：预算充足时
// 全链可达（第一环失败切第二环成功）。
func TestChainBudgetSufficientReachesAllEntries_Issue186(t *testing.T) {
	b := NewBudget(100000)
	first := &fakeCompleter{name: "a", err: errors.New("boom")}
	second := &fakeCompleter{name: "b", out: "ok-from-backup"}
	chain := NewChain(WithCompleter("a", first), WithCompleter("b", second), WithBudget(b), WithRetries(0))
	out, err := chain.Complete(context.Background(), Request{Prompt: "hi", Stage: "R1"})
	if err != nil || out != "ok-from-backup" {
		t.Fatalf("预算充足时全链应可达: %v %q", err, out)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("两环节各应被调用一次: %d/%d", first.calls, second.calls)
	}
}

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
