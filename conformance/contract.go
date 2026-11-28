package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oagentkit/llm/port"
	"github.com/oekit/concurrency/breaker"
)

// Behavior 描述一个 Completer 在测试场景中应表现出的行为。
//
// 套件通过 Factory 以 Behavior 为入参构造 Completer，从而控制每个子测试的
// 失败形态（错误分类载体、成功输出），被测实现只需按 Behavior 实现即可。
type Behavior struct {
	// Output 成功时的返回（Err 为 nil 时生效）。
	Output string
	// Err 失败时的返回（nil = 成功）。应当原样或以 %w 形式透传给调用方，
	// 以保证 errors.Is/As 穿透——%v 包装会打断分类链，套件会判红。
	Err error
}

// Factory 按 Behavior 构造一个 port.Completer。
// 套件每个子测试调用一次，被测实现应在其中返回自己的 Completer 实例。
type Factory func(b Behavior) port.Completer

// Run 跑完整契约场景（7 个子测试，任何子测试失败即实现违反契约）。
//
// factory 每个场景按 Behavior 构造全新 Completer；被测 Completer 的
// 资源由调用方在 factory 内经 t.Cleanup 清理。
//
// 每个子测试覆盖一条必须跨实现一致的语义：
//
//  1. fallback_reason_classifiable    降级链每一跳失败原因必须可分类
//     （errors.As 穿透到 StatusError 等载体），不得静默换模型
//  2. budget_precheck_before_call     预算判定必须在调用前——ErrBudgetExhausted
//     拦截后 Completer 不得被调用（事后扣减判不合规）
//  3. classifier_ctx_cancel           ctx 取消错误必须判不可重试
//  4. classifier_ctx_timeout          ctx 超时错误必须判不可重试
//  5. classifier_retryable            非 ctx 错误默认可重试
//  6. empty_chain_sentinel            空链返回 ErrEmptyChain
//  7. circuit_open_sentinel           熔断打开返回 ErrCircuitOpen
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("fallback_reason_classifiable", func(t *testing.T) { runFallbackReason(t, factory) })
	t.Run("budget_precheck_before_call", func(t *testing.T) { runBudgetPrecheck(t, factory) })
	t.Run("classifier_ctx_cancel", func(t *testing.T) { runClassifierCtxCancel(t, factory) })
	t.Run("classifier_ctx_timeout", func(t *testing.T) { runClassifierCtxTimeout(t, factory) })
	t.Run("classifier_retryable", func(t *testing.T) { runClassifierRetryable(t, factory) })
	t.Run("empty_chain_sentinel", func(t *testing.T) { runEmptyChain(t, factory) })
	t.Run("circuit_open_sentinel", func(t *testing.T) { runCircuitOpen(t, factory) })
}

// recordingCompleter 包装一个 Completer 并计数 Complete 调用次数。
type recordingCompleter struct {
	inner port.Completer
	calls *int
}

func (r *recordingCompleter) Complete(ctx context.Context, req port.Request) (string, error) {
	*r.calls++
	return r.inner.Complete(ctx, req)
}

// exhaustedBudget 是一个剩余预算为 0 的 TokenAccountant。
type exhaustedBudget struct{}

func (b *exhaustedBudget) Add(n int)      {}
func (b *exhaustedBudget) Used() int      { return 100 }
func (b *exhaustedBudget) Remaining() int { return 0 }

// runFallbackReason 场景 1：降级链每一跳失败原因必须可分类。
//
// 链上两环先后失败（429 限流 → 500 服务端故障），断言：
//   - 两环均被调用（降级确实发生，不是静默吞掉第一跳）；
//   - 出链错误可分类——errors.As 能穿透到 StatusError（原因不得被 %v 打断）。
func runFallbackReason(t *testing.T, factory Factory) {
	t.Helper()
	var calls1, calls2 int
	c1 := &recordingCompleter{
		inner: factory(Behavior{Err: &port.StatusError{Status: 429}}),
		calls: &calls1,
	}
	c2 := &recordingCompleter{
		inner: factory(Behavior{Err: &port.StatusError{Status: 500}}),
		calls: &calls2,
	}

	chain := port.NewChain(
		port.WithCompleter("first", c1),
		port.WithCompleter("second", c2),
	)

	_, err := chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if err == nil {
		t.Fatal("两环均失败时链应返回错误")
	}
	if calls1 != 1 {
		t.Fatalf("第一环应被调用恰 1 次，got %d", calls1)
	}
	if calls2 != 1 {
		t.Fatalf("第二环应被调用恰 1 次，got %d", calls2)
	}

	var se *port.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("出链错误必须可分类（errors.As 应穿透到 StatusError），got %v", err)
	}
	if se.Status != 500 {
		t.Fatalf("末跳错误应为 StatusError{Status:500}，got %d", se.Status)
	}
}

// runBudgetPrecheck 场景 2：预算判定必须在调用前。
//
// 预算耗尽时 Complete 必须返回 ErrBudgetExhausted 且 Completer 零调用——
// 事后扣减等于没有预算。
func runBudgetPrecheck(t *testing.T, factory Factory) {
	t.Helper()
	var calls int
	inner := factory(Behavior{Output: "ok"})
	c := &recordingCompleter{inner: inner, calls: &calls}

	chain := port.NewChain(
		port.WithCompleter("test", c),
		port.WithBudget(&exhaustedBudget{}),
	)

	_, err := chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if !errors.Is(err, port.ErrBudgetExhausted) {
		t.Fatalf("预算耗尽必须返回 ErrBudgetExhausted，got %v", err)
	}
	if calls != 0 {
		t.Fatalf("预算判定必须在调用前：Completer 不得被调用，got %d 次", calls)
	}
}

// runClassifierCtxCancel 场景 3a：ctx 取消 → 不可重试。
//
// Completer 返回 context.Canceled 时，链内分类结论必须是不可重试，
// 且结论随 RetryVerdictError 外泄（llm#227）。
func runClassifierCtxCancel(t *testing.T, factory Factory) {
	t.Helper()
	c := factory(Behavior{Err: context.Canceled})
	chain := port.NewChain(port.WithCompleter("test", c))

	_, err := chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if err == nil {
		t.Fatal("Completer 返回错误时链应返回错误")
	}
	verdict, ok := port.RetryVerdictOf(err)
	if !ok {
		t.Fatal("出链错误应携带 RetryVerdict（llm#227）")
	}
	if verdict.Retryable {
		t.Fatal("context.Canceled 必须判不可重试")
	}
}

// runClassifierCtxTimeout 场景 3b：ctx 超时 → 不可重试。
func runClassifierCtxTimeout(t *testing.T, factory Factory) {
	t.Helper()
	c := factory(Behavior{Err: context.DeadlineExceeded})
	chain := port.NewChain(port.WithCompleter("test", c))

	_, err := chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if err == nil {
		t.Fatal("Completer 返回错误时链应返回错误")
	}
	verdict, ok := port.RetryVerdictOf(err)
	if !ok {
		t.Fatal("出链错误应携带 RetryVerdict（llm#227）")
	}
	if verdict.Retryable {
		t.Fatal("context.DeadlineExceeded 必须判不可重试")
	}
}

// runClassifierRetryable 场景 3c：非 ctx 错误 → 可重试。
//
// 默认 Classifier 口径：除 ctx 取消/超时外都算可重试。
func runClassifierRetryable(t *testing.T, factory Factory) {
	t.Helper()
	c := factory(Behavior{Err: errors.New("transient")})
	chain := port.NewChain(port.WithCompleter("test", c))

	_, err := chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if err == nil {
		t.Fatal("Completer 返回错误时链应返回错误")
	}
	verdict, ok := port.RetryVerdictOf(err)
	if !ok {
		t.Fatal("出链错误应携带 RetryVerdict（llm#227）")
	}
	if !verdict.Retryable {
		t.Fatal("非 ctx 错误默认必须判可重试")
	}
}

// runEmptyChain 场景 4a：空链 → ErrEmptyChain。
// factory 不参与本场景断言（统一签名，_ 占位）。
func runEmptyChain(t *testing.T, _ Factory) {
	t.Helper()
	chain := port.NewChain()

	_, err := chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if !errors.Is(err, port.ErrEmptyChain) {
		t.Fatalf("空链必须返回 ErrEmptyChain，got %v", err)
	}
}

// runCircuitOpen 场景 4b：熔断打开 → ErrCircuitOpen。
//
// 用 WithConsecutiveFailures(1) 把阈值压到 1 次即跳开，第二次调用必须
// 以 ErrCircuitOpen 快速失败（不再触达 Completer）。
func runCircuitOpen(t *testing.T, factory Factory) {
	t.Helper()
	c := factory(Behavior{Err: errors.New("failure")})

	b := port.NewBreakers(
		breaker.WithConsecutiveFailures(1),
		breaker.WithCooldown(time.Minute),
	)
	chain := port.NewChain(
		port.WithCompleter("test", c),
		port.WithBreaker(b),
	)

	// 第一次调用：Completer 失败，熔断跳开。
	_, err := chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if err == nil {
		t.Fatal("Completer 返回错误时链应返回错误")
	}

	// 第二次调用：熔断已打开，必须以 ErrCircuitOpen 快速失败。
	_, err = chain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if !errors.Is(err, port.ErrCircuitOpen) {
		t.Fatalf("熔断打开必须返回 ErrCircuitOpen，got %v", err)
	}
}
