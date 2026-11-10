package llm

// 2026-11-11 issue 清零战回归测试（#138-#143）：每条用例钉一个失效形态，
// 标题带 issue 号——回归时按号检索。

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/llmtest"
	"github.com/oagentkit/llm/port"
	"github.com/oekit/concurrency/breaker"
)

// --- #138 免费模型的双口径 ---

// freePricedProvider 显式免费的 Provider：ModelPricing 返回零价 + true。
type freePricedProvider struct {
	*llmtest.Provider
}

func (f freePricedProvider) ModelPricing() (Price, bool) { return Price{}, true }

// TestProviderPriceFreeIsExplicitViaPricedProvider_Issue138 「免费」的唯一合法
// 表达是 PricedProvider 返回 (Price{}, true)——CostPer1KTokens 的 (0,0) 是
// 「未指定」，恒回落价表，不再被旧文档「0 表示不计费」误导。
func TestProviderPriceFreeIsExplicitViaPricedProvider_Issue138(t *testing.T) {
	// 显式免费：零价也是价，不回落价表。
	free := pricedProviderStub{
		priceProviderStub: priceProviderStub{name: "gpt-4o"},
		price:             Price{}, ok: true,
	}
	got, ok := providerPrice(free, DefaultPriceTable())
	if !ok || got != (Price{}) {
		t.Fatalf("显式免费应为 (Price{}, true)，got=%v ok=%v", got, ok)
	}

	// (0,0) 未指定：模型名命中价表 → 回落价表（非零价），**不是**免费。
	unset := priceProviderStub{name: "gpt-4o"}
	got, ok = providerPrice(unset, DefaultPriceTable())
	if !ok {
		t.Fatal("未指定 + 价表命中应算已定价")
	}
	if got == (Price{}) {
		t.Fatal("价表命中价不应为零值——(0,0) 不是免费表达")
	}

	// 端到端：免费 Provider 经 Resilient 记账 → PricePriced 且成本 0，
	// 不落 PriceUnpriced（免费与未知可区分）。
	p := &llmtest.Provider{
		Label:      "free",
		ModelValue: &llmtest.Model{Script: []llmtest.Resp{{Content: "ok", Prompt: 100, Completion: 50}}},
		CtxTokens:  128_000,
		MaxOut:     4096,
	}
	r, err := NewResilient(NewFallbackChain(freePricedProvider{p}), ResilientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewCostTracker()
	r.Tracker = tracker
	if _, err := r.Generate(context.Background(), "R1", []*schema.Message{schema.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	recs := tracker.Records()
	if len(recs) != 1 {
		t.Fatalf("应记 1 笔，实际 %d", len(recs))
	}
	if recs[0].PriceStatus != PricePriced {
		t.Fatalf("免费应记 PricePriced，实际 %v", recs[0].PriceStatus)
	}
	if recs[0].CostUSD != 0 {
		t.Fatalf("免费成本应为 0，实际 %v", recs[0].CostUSD)
	}
}

// --- #139 估算漏计 ---

// TestEstimateMsgTokensCountsToolCallsAndMultiContent_Issue139 无 usage 估算
// 必须计入 ToolCalls（工具名 + 参数 JSON）与多模态文本部件——大入参工具调用
// 只算正文会系统性低估，预算短路形同虚设（口径对齐 textx/compact 的 msgTokens）。
func TestEstimateMsgTokensCountsToolCallsAndMultiContent_Issue139(t *testing.T) {
	args := strings.Repeat("a", 300)
	m := &schema.Message{
		Role:    schema.Assistant,
		Content: "hello",
		ToolCalls: []schema.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: schema.FunctionCall{Name: "write_file", Arguments: args},
		}},
	}
	want := estimateTokens("hello") + estimateTokens("write_file") + estimateTokens(args)
	if got := estimateMsgTokens([]*schema.Message{m}); got != want {
		t.Fatalf("ToolCalls 应计入: got=%d want=%d", got, want)
	}

	// 多模态文本部件（三组字段）计入。
	multi := &schema.Message{
		Role:                  schema.User,
		UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: "看这张图"}},
	}
	if got := estimateMsgTokens([]*schema.Message{multi}); got != estimateTokens("看这张图") {
		t.Fatalf("UserInputMultiContent 文本应计入: got=%d", got)
	}

	// 旧口径对照：纯正文消息估算不变（防修复误伤既有口径）。
	plain := &schema.Message{Role: schema.User, Content: "plain"}
	if got := estimateMsgTokens([]*schema.Message{plain}); got != estimateTokens("plain") {
		t.Fatalf("纯正文估算漂移: got=%d want=%d", got, estimateTokens("plain"))
	}

	// nil 消息安全。
	if got := estimateMsgTokens([]*schema.Message{nil}); got != 0 {
		t.Fatalf("nil 消息应为 0: got=%d", got)
	}
}

// TestAccountEstimationIncludesToolCallArgs_Issue139 account 级：无 usage 回传 +
// 带大入参工具调用的消息，OnUsage 的 prompt 估算必须含入参。
func TestAccountEstimationIncludesToolCallArgs_Issue139(t *testing.T) {
	args := strings.Repeat("x", 900)
	c := NewClient(&llmtest.Model{Script: []llmtest.Resp{{Content: "ok", ToolCalls: llmtest.ToolCall("t", "{}")}}}, "m", nil)
	var prompt int
	c.OnUsage = func(_ string, p, _ int) { prompt = p }
	msgs := []*schema.Message{{
		Role:    schema.User,
		Content: "hi",
		ToolCalls: []schema.ToolCall{{
			Function: schema.FunctionCall{Name: "write_file", Arguments: args},
		}},
	}}
	c.account("R1", msgs, nil)
	if want := estimateTokens("hi") + estimateTokens("write_file") + estimateTokens(args); prompt != want {
		t.Fatalf("prompt 估算应含工具入参: got=%d want=%d", prompt, want)
	}
}

// --- #140 nil Provider 构造期 fail-fast ---

// TestNewResilientRejectsNilChainElements_Issue140 链上 nil / typed-nil 元素
// 构造期报错——不再把 panic 留给调用方 goroutine（queue#71 同款纪律）。
func TestNewResilientRejectsNilChainElements_Issue140(t *testing.T) {
	cfg := ResilientConfig{BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}

	if _, err := NewResilient(NewFallbackChain(nil), cfg); err == nil {
		t.Fatal("nil 元素应构造期报错")
	}
	// typed-nil：接口非 nil、指针 nil——方法内解引用字段会 panic 的形态。
	if _, err := NewResilient(NewFallbackChain((*llmtest.Provider)(nil)), cfg); err == nil {
		t.Fatal("typed-nil 元素应构造期报错")
	}
	// 正常链不受影响，错误文案带位置序号。
	ok, err := NewResilient(NewFallbackChain(llmtest.NewScriptedProvider("a")), cfg)
	if err != nil {
		t.Fatalf("正常链不应报错: %v", err)
	}
	if ok == nil {
		t.Fatal("应返回实例")
	}
	_, err = NewResilient(NewFallbackChain(llmtest.NewScriptedProvider("a"), nil), cfg)
	if err == nil || !strings.Contains(err.Error(), "第 1 个") {
		t.Fatalf("错误文案应带元素序号: %v", err)
	}
}

// --- #141 一次领取一次 Failure ---

type failingCompleter struct {
	mu    sync.Mutex
	calls int
}

func (f *failingCompleter) Complete(context.Context, port.Request) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return "", errors.New("boom")
}

// TestChainAttemptOneFailurePerAcquisition_Issue141 Chain 与 Resilient 的熔断
// 记账口径对齐：一次领取（内含 N 次物理重试）只报一次 Failure——阈值 3 的
// 熔断器不再被单个逻辑请求整体烧穿。
func TestChainAttemptOneFailurePerAcquisition_Issue141(t *testing.T) {
	fc := &failingCompleter{}
	brk := port.NewBreakers(breaker.WithConsecutiveFailures(3))
	c := port.NewChain(
		port.WithCompleter("a", fc),
		port.WithRetries(2), // 每次领取 3 次物理尝试
		port.WithBreaker(brk),
		port.WithBackoff(time.Millisecond, 2*time.Millisecond),
	)
	ctx := context.Background()
	for i := 1; i <= 2; i++ {
		if _, err := c.Complete(ctx, port.Request{Prompt: "p"}); err == nil {
			t.Fatalf("第 %d 次领取应失败", i)
		}
		if fc.calls != 3*i {
			t.Fatalf("第 %d 次领取应含 3 次物理尝试，实际 %d", i, fc.calls)
		}
		if _, ok := brk.Allow(ctx, "a"); !ok {
			t.Fatalf("第 %d 次领取（各报 1 次 Failure）后不应熔断", i)
		}
	}
	// 第 3 次领取报第 3 次 Failure → 熔断打开。
	if _, err := c.Complete(ctx, port.Request{Prompt: "p"}); err == nil {
		t.Fatal("第 3 次领取应失败")
	}
	if _, ok := brk.Allow(ctx, "a"); ok {
		t.Fatal("连续 3 次领取失败后应熔断")
	}
}

// --- #142 boost 粘性 ---

// maxTokensRecorder 记录每次调用实际生效的 MaxTokens（经 model.GetCommonOptions
// 解出——与 eino 真实接线同口径，不猜 opts 切片内容）。
type maxTokensRecorder struct {
	llmtest.Model
	mu      sync.Mutex
	perCall []int
}

func (m *maxTokensRecorder) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	o := model.GetCommonOptions(&model.Options{}, opts...)
	mt := 0
	if o.MaxTokens != nil {
		mt = *o.MaxTokens
	}
	m.mu.Lock()
	m.perCall = append(m.perCall, mt)
	m.mu.Unlock()
	return m.Model.Generate(ctx, in)
}

// TestGenerateRetryBoostStickyAcrossTransientRetries_Issue142 提升轮撞瞬态失败
// 后，续命重试轮保持提升后的输出上限——不再静默退回原上限再截断，把「提升过
// 一次」误读成「提升后仍截断」。
func TestGenerateRetryBoostStickyAcrossTransientRetries_Issue142(t *testing.T) {
	rm := &maxTokensRecorder{Model: llmtest.Model{Script: []llmtest.Resp{
		{FinishReason: "length"},                       // 截断 → 提升 1.5×
		{Err: errors.New("llm: upstream 503")},         // 提升轮撞瞬态失败（可重试）
		{Content: "ok"},                                // 续命重试轮成功
	}}}
	c := NewClient(rm, "m", nil)
	c.MaxOutputTokens = 100
	c.MaxRetries = 3
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 2 * time.Millisecond

	out, attempts, err := c.generateRetry(context.Background(), "R1",
		[]*schema.Message{schema.UserMessage("hi")}, c.retryPolicy())
	if err != nil {
		t.Fatalf("第 3 轮应成功: attempts=%d err=%v", attempts, err)
	}
	if out == nil || out.Content != "ok" {
		t.Fatalf("应返回成功响应: %+v", out)
	}
	rm.mu.Lock()
	perCall := append([]int(nil), rm.perCall...)
	rm.mu.Unlock()
	if len(perCall) != 3 {
		t.Fatalf("应 3 次物理调用，实际 %d: %v", len(perCall), perCall)
	}
	if perCall[0] != 0 {
		t.Fatalf("首轮不应携带 boost: %v", perCall)
	}
	if perCall[1] != 150 || perCall[2] != 150 {
		t.Fatalf("提升后每轮都应携带 150（粘性），实际 %v", perCall)
	}
}

// TestGenerateRetryBreaksAfterBoostedTruncation_Issue142 提升轮真的再截断才收手
// ——粘性不能把「提升后仍截断」的止损短路掉。
func TestGenerateRetryBreaksAfterBoostedTruncation_Issue142(t *testing.T) {
	rm := &maxTokensRecorder{Model: llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{FinishReason: "length"},
	}}}
	c := NewClient(rm, "m", nil)
	c.MaxOutputTokens = 100
	c.MaxRetries = 5
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 2 * time.Millisecond

	_, attempts, err := c.generateRetry(context.Background(), "R1",
		[]*schema.Message{schema.UserMessage("hi")}, c.retryPolicy())
	if err == nil {
		t.Fatal("持续截断应上抛错误")
	}
	if attempts != 2 {
		t.Fatalf("截断→提升→仍截断应收手（2 次物理调用），实际 %d", attempts)
	}
}

// --- #143 注入 HTTPClient 后链层不叠加 Timeout ---

// TestOpenAIProviderAttemptTimeoutZeroWithInjectedHTTPClient_Issue143 注入
// HTTPClient 后 AttemptTimeout() 恒 0——Resilient 跳过 per-attempt 截止，
// 超时归注入方 own（「本仓不再叠加自己的 Timeout」对链层同样成立）。
func TestOpenAIProviderAttemptTimeoutZeroWithInjectedHTTPClient_Issue143(t *testing.T) {
	ctx := context.Background()
	// 注入 + Timeout 留 0：链层无隐形 60s 上限。
	p, err := NewOpenAIProvider(ctx, OpenAIProviderConfig{
		BaseURL: "https://api.example.com/v1", APIKey: "k", Model: "m",
		HTTPClient: &http.Client{Timeout: 5 * time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.AttemptTimeout(); got != 0 {
		t.Fatalf("注入 HTTPClient 后 AttemptTimeout 应为 0（超时归注入方），实际 %v", got)
	}
	// 注入 + 显式 Timeout：同样不叠加到链层。
	p2, err := NewOpenAIProvider(ctx, OpenAIProviderConfig{
		BaseURL: "https://api.example.com/v1", APIKey: "k", Model: "m",
		Timeout: 7 * time.Second, HTTPClient: &http.Client{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p2.AttemptTimeout(); got != 0 {
		t.Fatalf("注入 HTTPClient 后显式 Timeout 也不该进链层，实际 %v", got)
	}
	// 未注入：链层缺省 60s 保留（既有行为）。
	p3, err := NewOpenAIProvider(ctx, OpenAIProviderConfig{
		BaseURL: "https://api.example.com/v1", APIKey: "k", Model: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p3.AttemptTimeout(); got != 60*time.Second {
		t.Fatalf("未注入时链层缺省应为 60s，实际 %v", got)
	}
}
