package llm

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/oekit/pkg/mathx"

	"github.com/oagentkit/llm/llmtest"
)

// rlErr429 标准限流错误文案（ClassifyLLMError 文本 marker 命中 429/rate limit）。
func rlErr429() error { return errors.New("429 too many requests: rate limited, throttle") }

// rlFastOpts 毫秒级退避 + 零抖动：测试只断言调用次数与成败，不睡真实退避窗口。
func rlFastOpts() []RateLimitRetryOption {
	return []RateLimitRetryOption{
		WithRetryAttempts(5),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryMaxDelay(5 * time.Millisecond),
		WithRetryJitterRatio(0),
	}
}

func rlMsgs() []*schema.Message { return []*schema.Message{schema.UserMessage("ping")} }

// Generate 路径：两次 429 退避后第三次成功——429 不再一次失败即硬错（llm#100）。
func TestRateLimitRetryGenerateRecovers(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{
		{Err: rlErr429()},
		{Err: rlErr429()},
		{Content: "ok"},
	}}
	m := WithRateLimitRetry(stub, rlFastOpts()...)
	out, err := m.Generate(context.Background(), rlMsgs())
	if err != nil {
		t.Fatalf("429 退避后应成功: %v", err)
	}
	if out.Content != "ok" {
		t.Fatalf("内容不符: %v", out.Content)
	}
	if stub.Calls != 3 {
		t.Fatalf("应恰好 3 次物理调用（429+429+成功）, 实际 %d", stub.Calls)
	}
}

// bianque 生产形态：5 次尝试内恢复（4 次连续 429 后第 5 次成功）。
func TestRateLimitRetryBianqueShape(t *testing.T) {
	script := make([]llmtest.Resp, 0, 5)
	for range 4 {
		script = append(script, llmtest.Resp{Err: rlErr429()})
	}
	script = append(script, llmtest.Resp{Content: "ok"})
	stub := &llmtest.Model{Script: script}
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(5),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryMaxDelay(2*time.Millisecond),
		WithRetryJitterRatio(0.25))
	out, err := m.Generate(context.Background(), rlMsgs())
	if err != nil {
		t.Fatalf("5 次尝试内应恢复: %v", err)
	}
	if out.Content != "ok" || stub.Calls != 5 {
		t.Fatalf("第 5 次尝试应成功, calls=%d out=%v", stub.Calls, out)
	}
}

// 非限流失败不归本装饰器管：401 原样快速上抛，不烧退避窗口。
func TestRateLimitRetryNonRateLimitFailsFast(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{
		{Err: errors.New("401 unauthorized: invalid api key")},
		{Content: "ok"},
	}}
	m := WithRateLimitRetry(stub, rlFastOpts()...)
	_, err := m.Generate(context.Background(), rlMsgs())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("非限流错误应原样上抛: %v", err)
	}
	if stub.Calls != 1 {
		t.Fatalf("非限流失败不得重试, 实际 %d 次调用", stub.Calls)
	}
}

// 退避耗尽：恰好 attempts 次物理调用，错误链保留（errors.Is 直达原始错误）。
func TestRateLimitRetryExhausts(t *testing.T) {
	boom := rlErr429()
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: boom}}, RepeatLast: true}
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(3),
		WithRetryBaseDelay(time.Millisecond),
		WithRetryMaxDelay(2*time.Millisecond),
		WithRetryJitterRatio(0))
	_, err := m.Generate(context.Background(), rlMsgs())
	if err == nil {
		t.Fatal("耗尽后应上抛错误")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("耗尽错误应保留原始错误链: %v", err)
	}
	if stub.Calls != 3 {
		t.Fatalf("attempts=3 应恰好 3 次调用, 实际 %d", stub.Calls)
	}
}

// 调用方取消：退避等待立即让位 ctx，不再烧后续尝试。
func TestRateLimitRetryContextCancel(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{{Err: rlErr429()}}, RepeatLast: true}
	ctx, cancel := context.WithCancel(context.Background())
	m := WithRateLimitRetry(stub,
		WithRetryAttempts(5),
		WithRetryBaseDelay(50*time.Millisecond),
		WithRetryMaxDelay(100*time.Millisecond),
		WithRetryJitterRatio(0))
	go func() {
		time.Sleep(10 * time.Millisecond) // 首次失败落进退避等待后取消
		cancel()
	}()
	_, err := m.Generate(ctx, rlMsgs())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应原样上抛 context.Canceled: %v", err)
	}
	if stub.Calls > 2 {
		t.Fatalf("取消后不得继续重试, 实际 %d 次调用", stub.Calls)
	}
}

// Stream 路径：建流前的 429 同样退避重试；流一旦建立即原样交还调用方
// （生命周期归消费方，本装饰器不触碰流——agent#77 的 exactly-once Close
// 纪律在本层之外）。
func TestRateLimitRetryStreamRecovers(t *testing.T) {
	stub := &llmtest.Model{Script: []llmtest.Resp{
		{Err: rlErr429()},
		{Content: "stream-ok"},
	}}
	m := WithRateLimitRetry(stub, rlFastOpts()...)
	sr, err := m.Stream(context.Background(), rlMsgs())
	if err != nil {
		t.Fatalf("流式 429 退避后应成功建流: %v", err)
	}
	defer sr.Close()
	var got string
	for {
		msg, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			t.Fatalf("流消费失败: %v", rerr)
		}
		got += msg.Content
	}
	if got != "stream-ok" {
		t.Fatalf("流内容不符: %q", got)
	}
	if stub.Calls != 2 {
		t.Fatalf("建流前 429 应重试一次, 实际 %d 次调用", stub.Calls)
	}
}

// WithTools 派生转发契约：派生实例沿用同一退避配置（对齐 breaker/限速装饰器家族）。
func TestRateLimitRetryWithToolsDerives(t *testing.T) {
	parent := &llmtest.Model{Script: []llmtest.Resp{
		{Err: rlErr429()},
		{Content: "tool-ok"},
	}}
	inner := &llmtest.ToolModel{Model: parent}
	m := WithRateLimitRetry(inner, rlFastOpts()...)
	tcm, ok := m.(einomodel.ToolCallingChatModel)
	if !ok {
		t.Fatalf("装饰器应实现 ToolCallingChatModel, 实际 %T", m)
	}
	bound, err := tcm.WithTools([]*schema.ToolInfo{{Name: "f"}})
	if err != nil {
		t.Fatalf("派生失败: %v", err)
	}
	out, err := bound.Generate(context.Background(), rlMsgs())
	if err != nil {
		t.Fatalf("派生实例应保持退避（429 后重试成功）: %v", err)
	}
	if out.Content != "tool-ok" {
		t.Fatalf("派生实例响应不符: %v", out.Content)
	}
	if parent.Calls != 2 {
		t.Fatalf("派生实例应消费父桩脚本两次（429+成功）, 实际 %d", parent.Calls)
	}
}

// IsCallbacksEnabled 转发内层判定（llm#86 纪律：装饰器自燃会造成 usage 双计）。
func TestRateLimitRetryCallbacksForwarding(t *testing.T) {
	inner := &cbEnabledModel{}
	m := WithRateLimitRetry(inner)
	cm, ok := m.(components.Checker)
	if !ok {
		t.Fatalf("装饰器应实现 components.Checker, 实际 %T", m)
	}
	if !cm.IsCallbacksEnabled() {
		t.Fatal("内层启用回调时装饰器应转发 true")
	}
}

type cbEnabledModel struct {
	llmtest.Model
}

func (m *cbEnabledModel) IsCallbacksEnabled() bool { return true }

// 内层不支持工具绑定时 WithTools 报错而非静默丢绑定。
func TestRateLimitRetryWithToolsUnsupported(t *testing.T) {
	m := WithRateLimitRetry(&llmtest.Model{})
	tcm, ok := m.(einomodel.ToolCallingChatModel)
	if !ok {
		t.Fatalf("装饰器应实现 ToolCallingChatModel, 实际 %T", m)
	}
	if _, err := tcm.WithTools([]*schema.ToolInfo{{Name: "f"}}); err == nil {
		t.Fatal("内层不支持工具绑定时应报错")
	}
}

// 缺省曲线 = bianque 生产实证形态；显式零抖动不得被缺省覆盖（测试确定性依赖）。
func TestRateLimitRetryDefaults(t *testing.T) {
	// 与 WithRateLimitRetry 构造同形态（jitterRatio=-1 哨兵 = 未设置）
	cfg := rateLimitRetryConfig{jitterRatio: -1}
	cfg.fillDefaults()
	if cfg.attempts != defaultRetryAttempts ||
		cfg.base != defaultRetryBaseDelay ||
		cfg.ceil != defaultRetryMaxDelay ||
		cfg.jitterRatio != defaultRetryJitterRatio {
		t.Fatalf("缺省曲线不符: %+v", cfg)
	}
	var explicit rateLimitRetryConfig
	WithRetryJitterRatio(0)(&explicit)
	WithRetryAttempts(2)(&explicit)
	explicit.fillDefaults()
	if explicit.jitterRatio != 0 {
		t.Fatalf("显式零抖动被缺省覆盖: %v", explicit.jitterRatio)
	}
	if explicit.attempts != 2 {
		t.Fatalf("显式 attempts 被改写: %v", explicit.attempts)
	}
}

// 退避曲线与 Client.generateRetry 同源（backoffDelayJittered 单源实现）：
// 零抖动下 429 下限 5s（钳到 ceil）、指数 2^n、封顶不越界。
func TestBackoffDelayJitteredCurve(t *testing.T) {
	det := mathx.NewUnstable(0)
	cases := []struct {
		name        string
		attempt     int
		base, ceil  time.Duration
		isRateLimit bool
		want        time.Duration
	}{
		{"非429首拍=base", 1, 2 * time.Second, 32 * time.Second, false, 2 * time.Second},
		{"429下限5s", 1, 2 * time.Second, 32 * time.Second, true, 5 * time.Second},
		{"4s仍被下限抬到5s", 2, 2 * time.Second, 32 * time.Second, true, 5 * time.Second},
		{"第3拍越过下限", 3, 2 * time.Second, 32 * time.Second, true, 8 * time.Second},
		{"第5拍封顶", 5, 2 * time.Second, 32 * time.Second, true, 32 * time.Second},
		{"封顶不越界", 6, 2 * time.Second, 32 * time.Second, true, 32 * time.Second},
		{"ceil低于下限时钳到ceil", 1, 2 * time.Second, 3 * time.Second, true, 3 * time.Second},
	}
	for _, tc := range cases {
		if got := backoffDelayJittered(tc.attempt, tc.base, tc.ceil, tc.isRateLimit, det); got != tc.want {
			t.Fatalf("%s: backoffDelayJittered(%d,%v,%v,%v)=%v 期望 %v",
				tc.name, tc.attempt, tc.base, tc.ceil, tc.isRateLimit, got, tc.want)
		}
	}
	// 包级 backoffDelay（0.5 单边向上抖动）＝同源曲线 + [d, 1.5d) 抖动：
	// 低档位结果落在区间内；封顶档（llm#189 起）为软上限 [ceil, ceil×1.5)——
	// 此前「高档位恒等于 ceil」是被钳制副作用钉死的同值行为，恰是 429 群撞
	// 复活的根源，不再成立。
	if got := backoffDelay(3, 2*time.Second, 32*time.Second, true); got < 8*time.Second || got >= 12*time.Second {
		t.Fatalf("backoffDelay 第3拍应落在 [8s,12s): %v", got)
	}
	if got := backoffDelay(5, 2*time.Second, 32*time.Second, true); got < 32*time.Second || got >= 48*time.Second {
		t.Fatalf("封顶档应落在软上限 [32s,48s): %v", got)
	}
}
