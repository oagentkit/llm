package llm

// #209 回归：RateLimitRetryModel 的重试观测钩子/类型化耗尽出口/谓词覆盖。
// 三个面都是宿主收编本地 RetryModel 的必需契约：
//   - OnRetry 必须在退避等待结束后、下一次物理调用发起前触发（决定重试时点
//     回调会在等待期 ctx 取消时谎报「已重试」）；
//   - 耗尽必须返回 *RetryExhaustedError（errors.As 可判、errors.Is 穿透），
//     且自报 Retryable()==true（宿主 toolfail.Classify 的鸭子契约）；
//   - 判定面可被 WithRetryPredicate 覆盖，缺省仍是 IsRateLimitError。

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/oagentkit/llm/llmtest"
)

// TestRateLimitRetryOnRetryAfterWaitBeforeCall_Issue209 钩子时点与入参：
// attempt = 即将发起的物理调用序号（1 基，取 2..N）；触发时物理调用数恰为
// attempt-1（等待已结束、本次调用尚未发起）；lastErr = 上一次物理失败原值。
func TestRateLimitRetryOnRetryAfterWaitBeforeCall_Issue209(t *testing.T) {
	first := errors.New("429 rate limit: first")
	second := errors.New("429 rate limit: second")
	stub := &llmtest.Model{Script: []llmtest.Resp{
		{Err: first},
		{Err: second},
		{Content: "ok"},
	}}
	var attempts []int
	var lastErrs []error
	m := WithRateLimitRetry(stub, append(rlFastOpts(),
		WithOnRetry(func(attempt int, lastErr error) {
			if stub.Calls != attempt-1 {
				t.Errorf("OnRetry(attempt=%d) 应在第 %d 次物理调用前触发, 实际已调用 %d 次",
					attempt, attempt, stub.Calls)
			}
			attempts = append(attempts, attempt)
			lastErrs = append(lastErrs, lastErr)
		}))...)
	out, err := m.Generate(context.Background(), rlMsgs())
	if err != nil {
		t.Fatalf("两次 429 后应恢复: %v", err)
	}
	if out.Content != "ok" {
		t.Fatalf("内容不符: %v", out.Content)
	}
	if want := []int{2, 3}; !slices.Equal(attempts, want) {
		t.Fatalf("OnRetry 应恰在重试拍触发 %v, 实际 %v", want, attempts)
	}
	if len(lastErrs) != 2 || !errors.Is(lastErrs[0], first) || !errors.Is(lastErrs[1], second) {
		t.Fatalf("OnRetry 应收到上一次物理失败原值: %v", lastErrs)
	}
	if stub.Calls != 3 {
		t.Fatalf("应 3 次物理调用, 实际 %d", stub.Calls)
	}
}

// TestRateLimitRetryOnRetryNotFiredOnWaitCancel_Issue209 等待期 ctx 取消：
// 钩子不得触发（决定重试时点回调会在此处谎报已重试），耗尽回调亦不得触发。
func TestRateLimitRetryOnRetryNotFiredOnWaitCancel_Issue209(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: rlErr429()}}, RepeatLast: true}
	ctx, cancel := context.WithCancel(context.Background())
	var retryHook, exhaustedHook int
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(5),
		WithRetryBaseDelay(100*time.Millisecond),
		WithRetryMaxDelay(100*time.Millisecond),
		WithRetryJitterRatio(0),
		WithOnRetry(func(int, error) { retryHook++ }),
		WithOnExhausted(func(int, error) { exhaustedHook++ }),
	)
	// 首次失败落进退避等待后取消（AfterFunc 不引入裸 goroutine）
	time.AfterFunc(10*time.Millisecond, cancel)
	_, err := m.Generate(ctx, rlMsgs())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应原样上抛 context.Canceled: %v", err)
	}
	if retryHook != 0 {
		t.Fatalf("等待期取消不得触发 OnRetry（未发生重试）, 实际 %d 次", retryHook)
	}
	if exhaustedHook != 0 {
		t.Fatalf("等待期取消不是耗尽, OnExhausted 不得触发, 实际 %d 次", exhaustedHook)
	}
	if stub.Calls != 1 {
		t.Fatalf("取消后不得继续重试, 实际 %d 次调用", stub.Calls)
	}
}

// TestRateLimitRetryExhaustedTypedOnce_Issue209 耗尽出口：OnExhausted 恰一次、
// 错误可 errors.As 到 *RetryExhaustedError、errors.Is 穿透到末个原始错误、
// Retryable() 自报瞬态（toolfail.Classify 的鸭子契约）。
func TestRateLimitRetryExhaustedTypedOnce_Issue209(t *testing.T) {
	boom := rlErr429()
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: boom}}, RepeatLast: true}
	var retries, exhaustedCalls, exhaustedAttempts int
	var exhaustedLast error
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(3),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryMaxDelay(2*time.Millisecond),
		WithRetryJitterRatio(0),
		WithOnRetry(func(int, error) { retries++ }),
		WithOnExhausted(func(attempts int, lastErr error) {
			exhaustedCalls++
			exhaustedAttempts = attempts
			exhaustedLast = lastErr
		}),
	)
	_, err := m.Generate(context.Background(), rlMsgs())
	if err == nil {
		t.Fatal("耗尽后应上抛错误")
	}
	var ree *RetryExhaustedError
	if !errors.As(err, &ree) {
		t.Fatalf("耗尽错误应为 *RetryExhaustedError, 实际 %T: %v", err, err)
	}
	if ree.Attempts != 3 {
		t.Fatalf("Attempts 应为总物理尝试数 3, 实际 %d", ree.Attempts)
	}
	if !errors.Is(err, boom) || !errors.Is(ree.Last, boom) {
		t.Fatalf("errors.Is 应穿透到末个原始错误: %v", err)
	}
	if !ree.Retryable() {
		t.Fatal("RetryExhaustedError 应自报可重试（宿主 toolfail.Classify 判瞬态）")
	}
	if got, ok := errors.AsType[*RetryExhaustedError](err); !ok || got != ree {
		t.Fatalf("errors.AsType 应命中同一耗尽错误: ok=%v got=%v", ok, got)
	}
	if retries != 2 {
		t.Fatalf("OnRetry 应为 attempts-1=2 次, 实际 %d", retries)
	}
	if exhaustedCalls != 1 {
		t.Fatalf("OnExhausted 应恰一次, 实际 %d", exhaustedCalls)
	}
	if exhaustedAttempts != 3 {
		t.Fatalf("OnExhausted 应收到总尝试数 3, 实际 %d", exhaustedAttempts)
	}
	if !errors.Is(exhaustedLast, boom) {
		t.Fatalf("OnExhausted 应收到末个原始错误: %v", exhaustedLast)
	}
	if stub.Calls != 3 {
		t.Fatalf("attempts=3 应恰好 3 次调用, 实际 %d", stub.Calls)
	}
}

// TestRateLimitRetryPredicateOverride_Issue209 谓词覆盖判定面：缺省不认的
// 自定义错误可被谓词放行重试；429 也可被谓词收窄为不重试（原样上抛，
// 不产生耗尽错误、不触发耗尽回调）。
func TestRateLimitRetryPredicateOverride_Issue209(t *testing.T) {
	custom := errors.New("provider code X: transient")

	// 缺省判定面（IsRateLimitError）不认这个错误：一次调用即上抛。
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: custom}, {Content: "ok"}}}
	m := WithRateLimitRetry(stub, rlFastOpts()...)
	_, err := m.Generate(context.Background(), rlMsgs())
	if !errors.Is(err, custom) {
		t.Fatalf("缺省谓词应原样上抛非限流错误: %v", err)
	}
	if stub.Calls != 1 {
		t.Fatalf("缺省谓词不得重试自定义错误, 实际 %d 次调用", stub.Calls)
	}

	// 自定义谓词接管：同一错误重试一次后成功。
	stub = &llmtest.Model{Script: []llmtest.Resp{{Err: custom}, {Content: "ok"}}}
	m = WithRateLimitRetry(stub, append(rlFastOpts(),
		WithRetryPredicate(func(err error) bool { return errors.Is(err, custom) }))...)
	out, err := m.Generate(context.Background(), rlMsgs())
	if err != nil {
		t.Fatalf("谓词放行后应重试成功: %v", err)
	}
	if out.Content != "ok" || stub.Calls != 2 {
		t.Fatalf("谓词放行应重试一次, calls=%d out=%v", stub.Calls, out)
	}

	// 谓词收窄：429 被判不重试，原样上抛（非耗尽路径）。
	rate := rlErr429()
	var exhausted int
	stub = &llmtest.Model{Script: []llmtest.Resp{{Err: rate}, {Content: "ok"}}}
	m = WithRateLimitRetry(stub, append(rlFastOpts(),
		WithRetryPredicate(func(error) bool { return false }),
		WithOnExhausted(func(int, error) { exhausted++ }))...)
	_, err = m.Generate(context.Background(), rlMsgs())
	if !errors.Is(err, rate) {
		t.Fatalf("谓词收窄后应原样上抛 429: %v", err)
	}
	if errors.As(err, new(*RetryExhaustedError)) {
		t.Fatalf("非重试路径不得产出耗尽错误: %v", err)
	}
	if exhausted != 0 {
		t.Fatalf("非重试路径不得触发 OnExhausted, 实际 %d 次", exhausted)
	}
	if stub.Calls != 1 {
		t.Fatalf("谓词收窄后不得重试, 实际 %d 次调用", stub.Calls)
	}
}

// TestRetryExhaustedErrorNilReceiver_Issue209 typed-nil 安全：宿主分类器可能
// 对链上每一环直接调 Retryable()（guardx/toolfail 契约），nil 接收者不得
// 被报成可重试、Error 不得 panic。
func TestRetryExhaustedErrorNilReceiver_Issue209(t *testing.T) {
	var nilErr *RetryExhaustedError
	if nilErr.Retryable() {
		t.Fatal("typed-nil *RetryExhaustedError 的 Retryable 应为 false（toolfail 契约）")
	}
	if nilErr.Error() == "" {
		t.Fatal("typed-nil Error() 不应为空串")
	}
}
