package llm

// 2026-11-16 issue 清零战回归测试（#209 wrap/ratelimit_retry、#210 llm/errors）：
// 每条用例钉一个失效形态，标题带 issue 号——回归时按号检索。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	openai "github.com/meguminnnnnnnnn/go-openai"

	"github.com/oagentkit/llm/llmtest"
)

// --- #209 重试观测钩子 / 类型化耗尽 / 谓词注入 ---

// TestRateLimitRetryOnRetryHook_Issue209 OnRetry 钩子三契约：调用次数 =
// attempts-1（全序耗尽）；回调时点在退避等待结束后、下次物理调用发起前
// （钩子里 stub.Calls == 刚失败的 attempt 序号——若在「决定重试」时点回调，
// 钩子里的物理调用数会是 attempt 本身而非 attempt-1 的值）；等待期 ctx 取消
// 不触发（另测）。
func TestRateLimitRetryOnRetryHook_Issue209(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: rlErr429()}}, RepeatLast: true}
	var attempts []int
	var callsAtHook []int
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(4),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryMaxDelay(2*time.Millisecond),
		WithRetryJitterRatio(0),
		WithOnRetry(func(attempt int, lastErr error) {
			if lastErr == nil {
				t.Error("OnRetry lastErr 不应为 nil（触发本次重试的错误）")
			}
			attempts = append(attempts, attempt)
			callsAtHook = append(callsAtHook, stub.Calls)
		}))
	_, err := m.Generate(context.Background(), rlMsgs())
	if err == nil {
		t.Fatal("耗尽后应上抛错误")
	}
	if len(attempts) != 3 {
		t.Fatalf("attempts=4 全序耗尽应恰 3 次 OnRetry, 实际 %d 次 (%v)", len(attempts), attempts)
	}
	for i, want := range []int{1, 2, 3} {
		if attempts[i] != want {
			t.Fatalf("OnRetry[%d].attempt = %d, want %d（刚失败的 1 基序号）", i, attempts[i], want)
		}
		// 钩子时点证明：第 i 次重试回调时，物理调用数 = i（等待已结束、
		// 下一次物理调用尚未发起）。
		if callsAtHook[i] != want {
			t.Fatalf("OnRetry[%d] 时物理调用数 = %d, want %d（必须在下次物理调用前回调）", i, callsAtHook[i], want)
		}
	}
}

// TestRateLimitRetryOnRetryRecover_Issue209 成功恢复路径：OnRetry 次数 = 恢复
// 前的失败数（2 次 429 后成功 → 2 次），OnExhausted 不触发。
func TestRateLimitRetryOnRetryRecover_Issue209(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{
		{Err: rlErr429()},
		{Err: rlErr429()},
		{Content: "ok"},
	}}
	var retries int
	exhausted := 0
	m := WithRateLimitRetry(stub, append(rlFastOpts(),
		WithOnRetry(func(int, error) { retries++ }),
		WithOnExhausted(func(int, error) { exhausted++ }),
	)...)
	if _, err := m.Generate(context.Background(), rlMsgs()); err != nil {
		t.Fatalf("退避后应成功: %v", err)
	}
	if retries != 2 || exhausted != 0 {
		t.Fatalf("恢复路径: OnRetry=%d (want 2), OnExhausted=%d (want 0)", retries, exhausted)
	}
}

// TestRateLimitRetryOnRetryCtxCancel_Issue209 等待期取消 → OnRetry 不触发
// （等待让位后重试并未发生，回调即谎报）。
func TestRateLimitRetryOnRetryCtxCancel_Issue209(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: rlErr429()}}, RepeatLast: true}
	canceled := false
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(5),
		WithRetryBaseDelay(50*time.Millisecond),
		WithRetryMaxDelay(100*time.Millisecond),
		WithRetryJitterRatio(0),
		WithOnRetry(func(int, error) { canceled = true }))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond) // 首次失败落进退避等待后取消
		cancel()
	}()
	_, err := m.Generate(ctx, rlMsgs())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应原样上抛 context.Canceled: %v", err)
	}
	if canceled {
		t.Fatal("等待期 ctx 取消后不得触发 OnRetry（重试并未发生）")
	}
}

// TestRateLimitRetryExhaustedTyped_Issue209 类型化耗尽出口：OnExhausted 恰一次
// 且收 attempts/lastErr；错误可 errors.As(*RetryExhaustedError)、Retryable()
// 自报瞬态、errors.Is 穿透 Unwrap 直达原始错误；文案与既有 fmt 包装同族。
func TestRateLimitRetryExhaustedTyped_Issue209(t *testing.T) {
	boom := rlErr429()
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: boom}}, RepeatLast: true}
	var hookAttempts int
	var hookLast error
	hookCalls := 0
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(3),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryMaxDelay(2*time.Millisecond),
		WithRetryJitterRatio(0),
		WithOnExhausted(func(attempts int, lastErr error) {
			hookCalls++
			hookAttempts, hookLast = attempts, lastErr
		}))
	_, err := m.Generate(context.Background(), rlMsgs())
	if err == nil {
		t.Fatal("耗尽后应上抛错误")
	}
	if hookCalls != 1 {
		t.Fatalf("OnExhausted 应恰一次, 实际 %d 次", hookCalls)
	}
	if hookAttempts != 3 || !errors.Is(hookLast, boom) {
		t.Fatalf("OnExhausted 参数不符: attempts=%d (want 3), lastErr=%v", hookAttempts, hookLast)
	}
	var exhausted *RetryExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("耗尽错误应可 errors.As(*RetryExhaustedError): %T (%v)", err, err)
	}
	if exhausted.Attempts != 3 {
		t.Fatalf("RetryExhaustedError.Attempts = %d, want 3（总尝试次数，含首次）", exhausted.Attempts)
	}
	if !exhausted.Retryable() {
		t.Fatal("耗尽定型 = 瞬态失败，Retryable() 应为 true（鸭子契约）")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("耗尽错误应保留原始错误链（Unwrap→Last）: %v", err)
	}
	if !strings.Contains(err.Error(), "仍限流") {
		t.Fatalf("耗尽文案应与既有 fmt 包装同族: %q", err.Error())
	}
}

// TestRateLimitRetryPredicate_Issue209 谓词注入双面：缺省判定不认识的错误码
// 快速上抛不重试；注入谓词后同型错误进入重试面。同时钉「谓词可收窄」——
// 缺省认得的 429 文案在谓词 false 下也不重试。
type issue209Err struct{ code string }

func (e issue209Err) Error() string { return "gateway error code=" + e.code }

func TestRateLimitRetryPredicate_Issue209(t *testing.T) {
	rlCode := issue209Err{code: "RL-1006"} // 不含任何缺省 marker（429/rate limit/throttl）

	// 缺省判定：非限流 → 快速上抛。
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: rlCode}}, RepeatLast: true}
	m := WithRateLimitRetry(stub, rlFastOpts()...)
	if _, err := m.Generate(context.Background(), rlMsgs()); err == nil {
		t.Fatal("应上抛")
	}
	if stub.Calls != 1 {
		t.Fatalf("缺省判定不认识的错误不得重试, 实际 %d 次调用", stub.Calls)
	}

	// 注入谓词：宿主按自己的错误码体系把 RL-1006 划进重试面。
	stub2 := &llmtest.Model{Script: []llmtest.Resp{{Err: rlCode}}, RepeatLast: true}
	m2 := WithRateLimitRetry(stub2,
		WithRetryAttempts(3),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryMaxDelay(2*time.Millisecond),
		WithRetryJitterRatio(0),
		WithRetryPredicate(func(err error) bool {
			var e issue209Err
			return errors.As(err, &e) && e.code == "RL-1006"
		}))
	_, err := m2.Generate(context.Background(), rlMsgs())
	var exhausted *RetryExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("谓词放行后应重试至耗尽: %v", err)
	}
	if stub2.Calls != 3 {
		t.Fatalf("谓词放行应烧满 attempts 次, 实际 %d", stub2.Calls)
	}

	// 谓词收窄：缺省认得的 429 文案，谓词 false 下同样快速上抛。
	stub3 := &llmtest.Model{Script: []llmtest.Resp{{Err: rlErr429()}}, RepeatLast: true}
	m3 := WithRateLimitRetry(stub3,
		WithRetryAttempts(3),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryJitterRatio(0),
		WithRetryPredicate(func(error) bool { return false }))
	if _, err := m3.Generate(context.Background(), rlMsgs()); err == nil {
		t.Fatal("应上抛")
	}
	if stub3.Calls != 1 {
		t.Fatalf("谓词收窄后不得重试, 实际 %d 次调用", stub3.Calls)
	}
}

// TestRateLimitRetryExhaustedBackwardCompat_Issue209 缺省形态逐字不变：不注入
// 任何钩子/谓词时，耗尽错误仍是同一文案族 + errors.Is 链（现有消费方零破坏）。
func TestRateLimitRetryExhaustedBackwardCompat_Issue209(t *testing.T) {
	boom := rlErr429()
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: boom}}, RepeatLast: true}
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(2),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryJitterRatio(0))
	_, err := m.Generate(context.Background(), rlMsgs())
	if err == nil {
		t.Fatal("耗尽后应上抛错误")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("errors.Is 原始错误链必须保持: %v", err)
	}
	if want := "llm: 429 退避重试 1 次仍限流: "; !strings.Contains(err.Error(), want) {
		t.Fatalf("耗尽文案应与历史 fmt 包装逐字同族: %q", err.Error())
	}
}

// --- #210 结构化状态码判定放宽（statusCoder 契约） ---

// issue210Err 非 OpenAI provider 的结构化限流错误：带状态码但既不是
// go-openai *RequestError、文案也不含任何文本 marker——旧判定面识别不出，
// 只能退回文本启发式（误判链 #64/#78/#114/#125）。
type issue210Err struct{ code int }

func (e issue210Err) Error() string { return "provider rejection" }

func (e issue210Err) HTTPStatusCode() int { return e.code }

// TestClassifyByHTTPStatusCustomCoder_Issue210 结构化通道可扩展：自定义
// statusCoder 经 fmt 包装后仍被结构化判定采信——429 判限流（重试）、401 判
// 确定性失败（不重试）、404 同为确定性；无状态码错误仍走文本兜底（现有黄金
// 用例不回归）；go-openai *RequestError 既有特例行为不变。
func TestClassifyByHTTPStatusCustomCoder_Issue210(t *testing.T) {
	cases := []struct {
		name                string
		err                 error
		wantRetryable       bool
		wantRateLimit       bool
		wantStructured      bool // 是否走结构化通道（文本 marker 命中与否的区分面）
		wantRateLimitByText bool // 无结构化码时文本兜底是否判限流
	}{
		{"429 结构化限流", fmt.Errorf("wrap: %w", issue210Err{code: 429}), true, true, true, false},
		{"401 结构化确定性", fmt.Errorf("wrap: %w", issue210Err{code: 401}), false, false, true, false},
		{"404 结构化确定性", issue210Err{code: 404}, false, false, true, false},
		{"双层包装穿透", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", issue210Err{code: 429})), true, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyLLMError(tc.err)
			if hint.Retryable != tc.wantRetryable || hint.IsRateLimit != tc.wantRateLimit {
				t.Fatalf("ClassifyLLMError(%v) = %+v, want Retryable=%v IsRateLimit=%v",
					tc.err, hint, tc.wantRetryable, tc.wantRateLimit)
			}
			// 结构化采信证明：错误文案不含任何 marker（"provider rejection"
			// 无 429/rate limit/throttl 字样），限流判定只能来自状态码。
			if hint.IsRateLimit && !tc.wantStructured {
				t.Fatalf("%v 不含文本 marker，限流判定应来自结构化状态码", tc.err)
			}
		})
	}

	// 文案含 marker 但无状态码 → 文本兜底照常（现有黄金用例不回归）。
	textOnly := errors.New("429 too many requests")
	if !IsRateLimitError(textOnly) {
		t.Fatal("无状态码错误仍应走文本兜底判限流")
	}
	if ClassifyLLMError(errors.New("plain network failure")).IsRateLimit {
		t.Fatal("无 marker 无状态码不得判限流")
	}

	// go-openai *RequestError 既有特例：字段形态未实现 HTTPStatusCode()，
	// 行为必须逐字不变。
	re := &openai.RequestError{HTTPStatusCode: 429, Err: errors.New("rate limited")}
	if !IsRateLimitError(fmt.Errorf("chain: %w", re)) {
		t.Fatal("go-openai RequestError 429 应仍判限流")
	}
	re401 := &openai.RequestError{HTTPStatusCode: 401, Err: errors.New("unauthorized")}
	if ClassifyLLMError(fmt.Errorf("chain: %w", re401)).Retryable {
		t.Fatal("go-openai RequestError 401 应判确定性失败不重试")
	}
}
