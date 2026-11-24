package port

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// hostRetryableReporter 宿主错误分类器（guardx/toolfail.RetryableReporter）的
// 契约形状。本仓**不 import** 那个仓：契约按**方法集**对接，故此处按同一
// 形状声明——宿主沿错误链 errors.As 找任意一环的 Retryable() 方法即可读到结论。
type hostRetryableReporter interface {
	Retryable() bool
}

// hostSelfReport 复刻宿主分类器的「自报」判据（guardx/toolfail.selfReported）：
// 沿错误链 errors.As 找自报者，返回 (结论, 是否问到)。
//
// 与宿主实现同形（`var r RetryableReporter; errors.As(err, &r)`）⇒ 宿主读到的
// 结论与本包内部判定必然同源。宿主的**类别**由它自己的策略表从这个 bool 推出，
// 不在本包判定面内，故此处只断言那一份结论（bool）。
func hostSelfReport(err error) (retryable bool, ok bool) {
	var r hostRetryableReporter
	if !errors.As(err, &r) {
		return false, false
	}
	return r.Retryable(), true
}

// TestRetryVerdictLeaksToHostClassifier 同一个错误 → llm 侧与宿主侧读到**同一份**
// 结论（llm#227）：llm 侧走 RetryVerdictOf，宿主侧走自报判据（toolfail 形态）。
// 两种取值都要覆盖——只测「可重试」会让「不可重试」那条路悄悄坏掉。
func TestRetryVerdictLeaksToHostClassifier(t *testing.T) {
	cases := []struct {
		name      string
		classify  Classifier
		wantRetry bool
	}{
		{"可重试", func(error) bool { return true }, true},
		{"不可重试", func(error) bool { return false }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boom := errors.New("上游 503")
			chain := NewChain(
				WithCompleter("a", &fakeCompleter{err: boom}),
				WithClassifier(tc.classify),
				WithRetries(0),
			)
			_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
			if err == nil {
				t.Fatal("应失败")
			}
			// llm 侧：直接透出口。
			v, ok := RetryVerdictOf(err)
			if !ok {
				t.Fatalf("出链错误应带重试判定结论: %v", err)
			}
			if v.Retryable != tc.wantRetry {
				t.Fatalf("llm 侧结论 %v，期望 %v", v.Retryable, tc.wantRetry)
			}
			// 宿主侧：toolfail 形态的自报判据。
			got, asked := hostSelfReport(err)
			if !asked {
				t.Fatalf("宿主应能从错误链上问到自报结论: %v", err)
			}
			if got != v.Retryable {
				t.Fatalf("宿主侧结论 %v 与 llm 侧 %v 不一致", got, v.Retryable)
			}
		})
	}
}

// TestRetryVerdictSurvivesHostWrapping 宿主把错误再包一层（%w）后，两侧仍读到
// 同一份结论——宿主分类器沿链找，不是只看最外层。
func TestRetryVerdictSurvivesHostWrapping(t *testing.T) {
	boom := errors.New("上游 500")
	chain := NewChain(
		WithCompleter("a", &fakeCompleter{err: boom}),
		WithClassifier(func(error) bool { return true }),
		WithRetries(0),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if err == nil {
		t.Fatal("应失败")
	}
	wrapped := fmt.Errorf("host layer: %w", err)

	v, ok := RetryVerdictOf(wrapped)
	if !ok || !v.Retryable {
		t.Fatalf("包一层后 llm 侧仍应读到可重试结论: %v %v", v, ok)
	}
	got, asked := hostSelfReport(wrapped)
	if !asked || !got {
		t.Fatalf("包一层后宿主侧仍应问到可重试结论: %v %v", got, asked)
	}
}

// TestRetryVerdictErrorPreservesIdentity 装饰**不改变错误身份**：errors.Is 仍穿透
// 到底层错误，文案不变（结论是结构面信息，不进文案）。
func TestRetryVerdictErrorPreservesIdentity(t *testing.T) {
	boom := errors.New("上游 500")
	chain := NewChain(
		WithCompleter("a", &fakeCompleter{err: boom}),
		WithClassifier(func(error) bool { return false }),
		WithRetries(0),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if !errors.Is(err, boom) {
		t.Fatalf("errors.Is 应穿透到底层错误，实际 %v", err)
	}
	if err.Error() != boom.Error() {
		t.Fatalf("文案应与底层错误一致，实际 %q", err.Error())
	}
}

// TestClassifierDrivesChainAndLeaksOnce 分类结论既驱动**链内决策**（重试还是切
// 下一环），又外泄给宿主；且每个失败只判一次（单源，不派生第二套）。
func TestClassifierDrivesChainAndLeaksOnce(t *testing.T) {
	boom := errors.New("上游 503")
	var calls int
	f := &fakeCompleter{err: boom}
	chain := NewChain(
		WithCompleter("a", f),
		WithClassifier(func(error) bool {
			calls++
			return true
		}),
		WithRetries(2),
		WithBackoff(time.Millisecond, 2*time.Millisecond),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if f.calls != 3 {
		t.Fatalf("可重试 → 环内重试 2 次 + 首次 = 3 次调用，实际 %d", f.calls)
	}
	if calls != f.calls {
		t.Fatalf("每个失败只判一次：分类 %d 次 vs 物理调用 %d 次", calls, f.calls)
	}
	v, ok := RetryVerdictOf(err)
	if !ok || !v.Retryable {
		t.Fatalf("出链错误应带可重试结论: %v %v", v, ok)
	}
}

// TestClassifierNonRetryableSwitchesEntry 不可重试 → 环内不重试、直接切下一环；
// 出链错误带「不可重试」结论，宿主侧读到同一份。
func TestClassifierNonRetryableSwitchesEntry(t *testing.T) {
	first := &fakeCompleter{err: errors.New("一级 400")}
	second := &fakeCompleter{err: errors.New("二级 400")}
	chain := NewChain(
		WithCompleter("a", first),
		WithCompleter("b", second),
		WithClassifier(func(error) bool { return false }),
		WithRetries(2),
		WithBackoff(time.Millisecond, 2*time.Millisecond),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if first.calls != 1 {
		t.Fatalf("不可重试 → 环内不重试，实际调用 %d 次", first.calls)
	}
	if second.calls != 1 {
		t.Fatalf("不可重试 → 切下一环后同样不重试，实际调用 %d 次", second.calls)
	}
	v, ok := RetryVerdictOf(err)
	if !ok {
		t.Fatalf("出链错误应带结论: %v", err)
	}
	if v.Retryable {
		t.Fatal("结论应为不可重试")
	}
	got, asked := hostSelfReport(err)
	if !asked || got {
		t.Fatalf("宿主侧应读到不可重试: %v %v", got, asked)
	}
}

// TestRetryVerdictErrorNilReceiver nil 接收者安全：宿主分类器会对链上每一环直接
// 调方法，链上可能挂着 typed-nil 指针 ⇒ 必须返回 false 且不解引用。
func TestRetryVerdictErrorNilReceiver(t *testing.T) {
	var e *RetryVerdictError
	if e.Retryable() {
		t.Fatal("nil 接收者应返回 false")
	}
	if e.Unwrap() != nil {
		t.Fatal("nil 接收者 Unwrap 应返回 nil")
	}
	if e.Error() != "<nil>" {
		t.Fatalf("nil 接收者 Error 应返回占位文案，实际 %q", e.Error())
	}
	// typed-nil 挂在链上：宿主自报判据不应 panic，且报「不可重试」。
	wrapped := fmt.Errorf("outer: %w", e)
	got, asked := hostSelfReport(wrapped)
	if !asked {
		t.Fatal("宿主应问到自报结论")
	}
	if got {
		t.Fatal("typed-nil 应报不可重试")
	}
}

// TestVerdictOfPureFunction 判据体是纯函数：nil 分类器 / nil 错误 → 保守缺省
// （不可重试）；同一输入恒得同一结论。
func TestVerdictOfPureFunction(t *testing.T) {
	if v := verdictOf(nil, errors.New("x")); v.Retryable {
		t.Fatal("nil 分类器应取保守缺省（不可重试）")
	}
	if v := verdictOf(defaultClassifier, nil); v.Retryable {
		t.Fatal("nil 错误不应有可重试结论")
	}
	c := func(error) bool { return true }
	err := errors.New("x")
	if verdictOf(c, err) != verdictOf(c, err) {
		t.Fatal("同一输入应恒得同一结论")
	}
}

// TestNoVerdictForUnclassifiedErrors 预算/熔断这类**链内控制**错误与用户主动中止
// 不经分类 ⇒ 不带结论（没判过的就没有结论可报，别拿一个默认结论糊弄宿主）。
func TestNoVerdictForUnclassifiedErrors(t *testing.T) {
	budget := NewBudget(10)
	budget.Add(10)
	chain := NewChain(
		WithCompleter("m", &fakeCompleter{out: "ok"}),
		WithBudget(budget),
	)
	_, err := chain.Complete(context.Background(), Request{Prompt: "hi"})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("应返回 ErrBudgetExhausted，实际 %v", err)
	}
	if _, ok := RetryVerdictOf(err); ok {
		t.Fatal("未分类的错误不应带结论")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = NewChain(WithCompleter("m", &fakeCompleter{out: "ok"})).
		Complete(ctx, Request{Prompt: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 context.Canceled，实际 %v", err)
	}
	if _, ok := RetryVerdictOf(err); ok {
		t.Fatal("用户主动中止不应带结论")
	}
}
