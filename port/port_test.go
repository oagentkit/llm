package port

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/oagentkit/textx/textutil"
)

// fakeCompleter 假模型：不需要真网络，按脚本返回或报错。
type fakeCompleter struct {
	name  string
	calls int
	// err 非空则本次调用失败。
	err error
	// out 成功时的返回文本。
	out string
}

func (f *fakeCompleter) Complete(ctx context.Context, req Request) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

// ---------- 降级链 ----------

// TestChainFallsBackToNextCompleter 第一个环节失败 → 切到第二个；
// 成功的那次调用要记在**实际执行的模型**上（成本跟随实际模型，与 eino 路径一致）。
func TestChainFallsBackToNextCompleter(t *testing.T) {
	table := DefaultPriceTable()
	models := table.Models()
	if len(models) < 2 {
		t.Skip("内建价表模型数不足")
	}
	primary, backup := models[0], models[1]

	boom := errors.New("上游 500")
	first := &fakeCompleter{name: primary, err: boom}
	second := &fakeCompleter{name: backup, out: "ok-from-backup"}

	tracker := NewCostTrackerWithTable(table)
	chain := NewChain(
		WithCompleter(primary, first),
		WithCompleter(backup, second),
		WithTracker(tracker),
		WithRetries(0),
	)
	out, err := chain.Complete(context.Background(), Request{Prompt: "hi", Stage: "R1"})
	if err != nil {
		t.Fatalf("降级链应成功: %v", err)
	}
	if out != "ok-from-backup" {
		t.Fatalf("应返回备选模型的结果，实际 %q", out)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("两个环节各应被调用一次: primary=%d backup=%d", first.calls, second.calls)
	}
	// 成本记在实际执行的模型上（backup），而不是失败的那个。
	recs := tracker.Records()
	if len(recs) != 1 {
		t.Fatalf("应有 1 条成本记录，实际 %d", len(recs))
	}
	if recs[0].Model != backup {
		t.Fatalf("成本应记在实际执行的模型 %q 上，实际 %q", backup, recs[0].Model)
	}
	if recs[0].PriceStatus != PricePriced {
		t.Fatalf("backup 若在价表里则应已计价，实际 %v", recs[0].PriceStatus)
	}
}

// TestChainAllFail 返回最后一个错误（不是第一个）。
func TestChainAllFail(t *testing.T) {
	e1 := errors.New("第一个错")
	e2 := errors.New("第二个错")
	chain := NewChain(
		WithCompleter("a", &fakeCompleter{err: e1}),
		WithCompleter("b", &fakeCompleter{err: e2}),
		WithRetries(0),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if !errors.Is(err, e2) {
		t.Fatalf("应返回最后一个错误，实际 %v", err)
	}
}

// TestChainRetriesWithinCompleter 同一环节内的重试次数。
func TestChainRetriesWithinCompleter(t *testing.T) {
	f := &fakeCompleter{err: errors.New("临时故障")}
	chain := NewChain(
		WithCompleter("a", f),
		WithRetries(2),
		WithBackoff(time.Millisecond, 2*time.Millisecond),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if err == nil {
		t.Fatal("应失败")
	}
	if f.calls != 3 {
		t.Fatalf("重试 2 次 + 首次 = 3 次调用，实际 %d", f.calls)
	}
}

// ---------- 预算 ----------

// TestChainBudgetExhaustedRejected 预算耗尽 → **在调用前**拦下（completer 一次都没被调用）。
func TestChainBudgetExhaustedRejected(t *testing.T) {
	f := &fakeCompleter{out: "不该被调用"}
	budget := NewBudget(10)
	// 先把预算用光
	budget.Add(10)

	chain := NewChain(WithCompleter("m", f), WithBudget(budget))
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("预算耗尽应返回 ErrBudgetExhausted，实际 %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("预算耗尽时不得调用模型，实际调用了 %d 次", f.calls)
	}
}

// TestChainBudgetAccumulates 成功调用后预算要累计消耗。
func TestChainBudgetAccumulates(t *testing.T) {
	f := &fakeCompleter{out: "回答"}
	budget := NewBudget(1000)
	chain := NewChain(WithCompleter("m", f), WithBudget(budget))
	if _, err := chain.Complete(context.Background(), Request{Prompt: "问题", System: "系统"}); err != nil {
		t.Fatal(err)
	}
	want := textutil.EstTokensCJK("系统") + textutil.EstTokensCJK("问题") + textutil.EstTokensCJK("回答")
	if budget.Used() != want {
		t.Fatalf("预算累计不符: got=%d want=%d", budget.Used(), want)
	}
}

// ---------- 成本 ----------

// TestChainRecordsCost 按价表算出成本（关系断言，不写死数值）。
func TestChainRecordsCost(t *testing.T) {
	table := DefaultPriceTable()
	models := table.Models()
	if len(models) == 0 {
		t.Skip("内建价表为空")
	}
	model := models[0]
	price, lk := table.Lookup(model)
	if !lk.Found() {
		t.Fatalf("%q 应在价表里", model)
	}

	tracker := NewCostTrackerWithTable(table)
	chain := NewChain(WithCompleter(model, &fakeCompleter{out: "答案"}), WithTracker(tracker))
	if _, err := chain.Complete(context.Background(), Request{Prompt: "问", Stage: "R1"}); err != nil {
		t.Fatal(err)
	}
	recs := tracker.Records()
	if len(recs) != 1 {
		t.Fatalf("应有 1 条记录，实际 %d", len(recs))
	}
	r := recs[0]
	if r.Model != model || r.Stage != "R1" {
		t.Fatalf("记录应带上模型与阶段: %+v", r)
	}
	want := price.CostOf(textutil.EstTokensCJK("问"), textutil.EstTokensCJK("答案"))
	if math.Abs(r.CostUSD-want) > 1e-9 {
		t.Fatalf("成本关系不成立: got=%v want=%v", r.CostUSD, want)
	}
	if tracker.UnpricedCount() != 0 {
		t.Fatalf("已知模型不该进未定价清单: %d", tracker.UnpricedCount())
	}
}

// TestChainUnknownModelUnpriced 窄接口路径上遇到未知模型 → 记「未定价」，
// **不能记 0**（0 成本与「免费模型」不可区分）。
func TestChainUnknownModelUnpriced(t *testing.T) {
	tracker := NewCostTracker()
	chain := NewChain(
		WithCompleter("totally-unknown-model-xyz", &fakeCompleter{out: "答案"}),
		WithTracker(tracker),
	)
	if _, err := chain.Complete(context.Background(), Request{Prompt: "问"}); err != nil {
		t.Fatal(err)
	}
	recs := tracker.Records()
	if len(recs) != 1 {
		t.Fatalf("应有 1 条记录，实际 %d", len(recs))
	}
	if recs[0].PriceStatus != PriceUnpriced {
		t.Fatalf("未知模型必须记未定价，实际 %v", recs[0].PriceStatus)
	}
	if tracker.UnpricedCount() != 1 {
		t.Fatalf("未定价计数应为 1，实际 %d", tracker.UnpricedCount())
	}
}

// TestChainExactTokenCounter 宿主注入精确计量后，按精确值记账。
func TestChainExactTokenCounter(t *testing.T) {
	tracker := NewCostTracker()
	chain := NewChain(
		WithCompleter("m", &fakeCompleter{out: "答案"}),
		WithTracker(tracker),
		WithTokenCounter(func(req Request, resp string) Usage {
			return Usage{PromptTokens: 7, CompletionTokens: 11}
		}),
	)
	if _, err := chain.Complete(context.Background(), Request{Prompt: "问"}); err != nil {
		t.Fatal(err)
	}
	recs := tracker.Records()
	if recs[0].PromptTokens != 7 || recs[0].CompletionTokens != 11 {
		t.Fatalf("应按精确计量记账: %+v", recs[0])
	}
}

// ---------- 错误分类与熔断 ----------

// TestClassifyStatus HTTP 状态码分类：429/5xx 可重试，其余 4xx 不可重试。
func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{429, true}, {500, true}, {503, true}, {400, false}, {401, false}, {404, false},
	}
	for _, c := range cases {
		err := &StatusError{Status: c.status, Err: errors.New("http")}
		if got := ClassifyStatus(err); got != c.want {
			t.Fatalf("HTTP %d: 期望可重试=%v，实际 %v", c.status, c.want, got)
		}
	}
	if ClassifyStatus(context.Canceled) {
		t.Fatal("ctx 取消不该算可重试")
	}
}

// TestChainNonRetryableSkipsRetries 不可重试的错误 → 立刻切下一环，不在原环节重试。
func TestChainNonRetryableSkipsRetries(t *testing.T) {
	bad := &StatusError{Status: 400, Err: errors.New("bad request")}
	f := &fakeCompleter{err: bad}
	ok := &fakeCompleter{out: "ok"}
	chain := NewChain(
		WithCompleter("a", f),
		WithCompleter("b", ok),
		WithRetries(3),
		WithBackoff(time.Millisecond, 2*time.Millisecond),
		WithClassifier(ClassifyStatus),
	)
	out, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("应切到下一环成功: %v", err)
	}
	if out != "ok" {
		t.Fatalf("应返回备选结果，实际 %q", out)
	}
	if f.calls != 1 {
		t.Fatalf("400 不可重试 → 原环节只该调 1 次，实际 %d", f.calls)
	}
}

// TestChainContextCancelNotBreaker 用户主动取消 → 原样上抛，且不熔断。
func TestChainContextCancelNotBreaker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeCompleter{err: context.Canceled}
	chain := NewChain(WithCompleter("a", f), WithRetries(2))
	_, err := chain.Complete(ctx, Request{Prompt: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应原样上抛，实际 %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("ctx 已取消时不应发起调用，实际 %d 次", f.calls)
	}
}

// TestChainEmptyChain 空链 → ErrEmptyChain。
func TestChainEmptyChain(t *testing.T) {
	_, err := NewChain().Complete(context.Background(), Request{Prompt: "hi"})
	if !errors.Is(err, ErrEmptyChain) {
		t.Fatalf("空链应返回 ErrEmptyChain，实际 %v", err)
	}
}
