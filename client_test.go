package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/llmtest"
)

func TestClassifyLLMError(t *testing.T) {
	cases := []struct {
		err       error
		retryable bool
		rateLimit bool
		truncated bool
	}{
		// nil 语义与 RetryableLLMError(nil)=false 对齐（llm#65）：nil 无失败可言，
		// ClassifyLLMError(nil) 不再返回 Retryable:true（成功路径误传 nil 会被
		// 当成「可重试的失败」）
		{nil, false, false, false},
		{errors.New("401 unauthorized"), false, false, false},
		{errors.New("invalid api key"), false, false, false},
		{errors.New("context length exceeded"), false, false, false},
		{errors.New("too many tokens"), false, false, false},
		{errors.New("429 rate limit"), true, true, false},
		{errors.New("too many requests"), true, true, false},
		{errors.New("finish_reason=length"), true, false, true},
		{errors.New("connection reset"), true, false, false},
		// 确定性 4xx：参数错/模型不存在，重试无意义
		{errors.New("error, status code: 400, message: max_tokens too large"), false, false, false},
		{errors.New("error, status code: 404, message: model not found"), false, false, false},
		{errors.New("400 bad request: invalid parameter"), false, false, false},
		{errors.New("unknown model: gpt-99"), false, false, false},
		// 408/429 仍可重试
		{errors.New("408 request timeout"), true, false, false},
		// 词边界：状态码不得命中耗时/计数等数字文案
		{errors.New("request failed after 1429ms"), true, false, false},
		// 词边界收紧（llm#78）：后边界排除字母——"400ms"/"401ms"/"429ms" 等
		// 带单位后缀的耗时文案不是状态码。此前只防相邻数字，"400ms" 的 "m"
		// 照样放行：耗时文案被判成确定性 4xx（单客户端直接放弃重试）或 429
		// 限流（白等限流退避、误采信 Retry-After）。
		{errors.New("upstream timeout after 400ms"), true, false, false},
		{errors.New("auth handshake failed after 401ms"), true, false, false},
		{errors.New("failed after 429ms"), true, false, false},
		// 前边界排除字母：词内成分（如标识符片段）同样不是状态码
		{errors.New("req a429b mismatch"), true, false, false},
		// 后边界排除「小数点+数字」（llm#114）："." 非字母数字曾按边界成立——
		// "429.53ms" 假阳性限流（白等退避）、"401.2" 假阳性确定性失败（放弃
		// 重试）；小数点后跟数字时是数字的一部分，不是边界。
		{errors.New("request timeout after 429.53ms"), true, false, false},
		{errors.New("parse row 401.2 malformed"), true, false, false},
		// 前侧镜像（llm#125）：marker 是某小数的分数尾——"." 前是数字，
		// marker 是数字的一部分，不是独立状态码。"elapsed 12.429 total" 曾
		// 假阳性限流（白等退避）、"12.401" 假阳性确定性失败（放弃重试）。
		{errors.New("elapsed 12.429 total"), true, false, false},
		{errors.New("elapsed 12.401 total"), true, false, false},
		// 句末句点（小数点后无数字）仍是边界：真状态码形态不受影响
		{errors.New("gateway error 429. retry later"), true, true, false},
		// 真状态码形态（空格/标点/括号边界）仍命中
		{errors.New("status 429, retry later"), true, true, false},
		{errors.New("gateway returned (400) invalid"), false, false, false},
		// 瞬态文案不得撞确定性 marker（llm#64）：网关超时撞 ctxLen 的裸
		// "too long"、DNS 瞬时失败撞 det4xx 的裸 "does not exist"——不重试
		// = 放弃自愈。收窄后均应判可重试。
		{errors.New("upstream took too long"), true, false, false},
		{errors.New("request took too long"), true, false, false},
		{errors.New(`lookup api.example.com on 10.0.0.10:53: read udp: name does not exist`), true, false, false},
		// 收窄后真 ctx/模型错误仍命中
		{errors.New("this model's maximum context length is 4097 tokens, however you asked for 8000"), false, false, false},
		{errors.New("input is too long for this model"), false, false, false},
		{errors.New("model does not exist: gpt-99"), false, false, false},
		{errors.New("batch 1401 done"), true, false, false},
		{errors.New("error, status code: 429, message: rate limited"), true, true, false},
		// 截断 marker 必须最先判：Client 自产截断错误文本含 completion_tokens=<n>，
		// n 恰为 4xx/429 值时不得被数字 marker 抢先误判（否则"截断→提升
		// MaxOutputTokens 重试"机制确定性失效）
		{errors.New(`llm: R1 输出被截断（finish_reason=length, completion_tokens=401）`), true, false, true},
		{errors.New(`llm: R1 输出被截断（finish_reason=length, completion_tokens=429）`), true, false, true},
		{errors.New(`llm: R1 输出被截断（finish_reason=length, completion_tokens=404）`), true, false, true},
	}
	for _, c := range cases {
		hint := ClassifyLLMError(c.err)
		if hint.Retryable != c.retryable {
			t.Errorf("%v: retryable 应为 %v", c.err, c.retryable)
		}
		if hint.IsRateLimit != c.rateLimit {
			t.Errorf("%v: rateLimit 应为 %v", c.err, c.rateLimit)
		}
		if hint.IsTruncated != c.truncated {
			t.Errorf("%v: truncated 应为 %v", c.err, c.truncated)
		}
	}
}

func TestBudgetConfig(t *testing.T) {
	cfg := BudgetConfig{ContextTokens: 1_000_000}

	if got := cfg.MaxDiffChars(); got != 1_400_000 {
		t.Errorf("MaxDiffChars = %d, want 1400000", got)
	}
	if got := cfg.TaskTokenBudget(); got != 600_000 {
		t.Errorf("TaskTokenBudget = %d, want 600000", got)
	}
	if got := cfg.MaxOutputTokens(); got != 50_000 {
		t.Errorf("MaxOutputTokens = %d, want 50000", got)
	}
	if got := cfg.ReqBodyMaxChars(); got != 60_000 {
		t.Errorf("ReqBodyMaxChars = %d, want 60000", got)
	}
	if got := cfg.ReqIssueMaxChars(); got != 15_000 {
		t.Errorf("ReqIssueMaxChars = %d, want 15000", got)
	}
}

func TestBudgetConfigMinimums(t *testing.T) {
	cfg := BudgetConfig{ContextTokens: 128_000}

	if got := cfg.MaxDiffChars(); got < 10_000 {
		t.Errorf("MaxDiffChars = %d, should be >= 10000", got)
	}
	if got := cfg.MaxOutputTokens(); got < 4096 {
		t.Errorf("MaxOutputTokens = %d, should be >= 4096", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	// CJK 感知口径（textutil.EstTokensCJK，v0.11.2 起）：CJK 计 2 单位、
	// tokens≈units/3+1——中文为主的输入不再系统性低估（v0.10.40 的纯 rune/3
	// 口径对中文低估约 3 倍）
	if got := estimateTokens("hello world"); got != 4 { // 11 units/3+1
		t.Errorf("estimateTokens('hello world') = %d, want 4", got)
	}
	if got := estimateTokens("你好世界"); got != 3 { // 8 units/3+1=3
		t.Errorf("estimateTokens('你好世界') = %d, want 3", got)
	}
}

func TestRetryableLLMError(t *testing.T) {
	if RetryableLLMError(nil) {
		t.Error("nil 无失败可言，不应重试（与 IsRateLimitError/IsTruncatedError 的 nil 语义一致）")
	}
	if RetryableLLMError(errors.New("401 unauthorized")) {
		t.Error("401 不应重试")
	}
	if !RetryableLLMError(errors.New("500 internal error")) {
		t.Error("500 应可重试")
	}
}

// TestGenerateJSONMisuseReturnsError 回归（v0.10.30 第四轮审计）：out 非指针/
// nil 指针返回可捕获错误而非 panic（reflect 改写曾把 InvalidUnmarshalError
// 退化成击穿 panic）。
func TestGenerateJSONMisuseReturnsError(t *testing.T) {
	c := NewClient(nil, "stub", nil)
	if err := c.GenerateJSON(context.Background(), "t", nil, struct{}{}); err == nil {
		t.Fatal("值类型 out 应报错")
	}
	var m map[string]any
	if err := c.GenerateJSON(context.Background(), "t", nil, m); err == nil {
		t.Fatal("非指针 out 应报错")
	}
	var ptr *struct{ A int }
	if err := c.GenerateJSON(context.Background(), "t", nil, ptr); err == nil {
		t.Fatal("nil 指针 out 应报错")
	}
}

// TestClientTruncatedNoDoomedRepeat 提升 MaxTokens 后仍截断：不再以同参数
// 重复注定失败的请求（第六轮审计——此前 truncatedBoosted 后继续按原参数重试，
// 白烧一次钱）。
func TestClientTruncatedNoDoomedRepeat(t *testing.T) {
	m := &llmtest.Model{Script: []llmtest.Resp{
		{Content: "截断输出", FinishReason: "length"},
		{Content: "仍截断", FinishReason: "length"},
		{Content: "不应到达的第三次", FinishReason: "length"},
	}}
	c := NewClient(m, "m", nil)
	c.MaxRetries = 3
	c.MaxOutputTokens = 1000
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 2 * time.Millisecond

	_, err := c.Generate(context.Background(), "T", msgs("hi"))
	if err == nil {
		t.Fatal("持续截断应报错")
	}
	if got := m.Calls; got != 2 {
		t.Fatalf("调用数 = %d，应为 2（原参数 1 次 + 提升 1 次，之后不再重复）", got)
	}
}

// TestClientTruncatedBoostRefitsInput（llm#83）：提升 MaxTokens 的重试轮按
// 1.5 倍输出预留重算输入预算再裁剪——贴近窗口的输入在提升轮被再次裁剪留痕，
// 而不是带着「1.5 倍输出上限 + 原输入 > 窗口」的组合打出去（二次尝试必以
// context too long 收场，还会被判成确定性失败、在降级链上白烧一次切换）。
func TestClientTruncatedBoostRefitsInput(t *testing.T) {
	m := &llmtest.Model{Script: []llmtest.Resp{
		{Content: "截断输出", FinishReason: "length"},
		{Content: "ok"},
	}, RepeatLast: true}
	c := NewClient(m, "m", nil)
	// 1 倍预留 limit = (1000-300)×3/2×0.9 = 945 runes：800 rune 输入首轮不裁；
	// 提升 1.5 倍（450 预留）后 limit = (1000-450)×3/2×0.9 = 742：提升轮必须重裁
	c.ContextTokens = 1000
	c.MaxOutputTokens = 300
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 2 * time.Millisecond

	long := strings.Repeat("x", 800)
	if _, err := c.Generate(context.Background(), "T", []*schema.Message{schema.UserMessage(long)}); err != nil {
		t.Fatalf("提升轮应成功: %v", err)
	}
	if m.Calls != 2 {
		t.Fatalf("调用数 = %d，应为 2（首轮截断 + 提升轮）", m.Calls)
	}
	if strings.Contains(m.Flats[0], "已截断留痕") {
		t.Fatal("首轮（945 预算）不应裁剪 800 rune 输入")
	}
	if !strings.Contains(m.Flats[1], "已截断留痕") {
		t.Fatalf("提升轮应按 742 预算重裁输入: %q", m.Flats[1])
	}
	if got := len([]rune(m.Flats[1])); got >= 800 {
		t.Fatalf("提升轮输入应收窄到 742 预算内，got %d runes", got)
	}
}

// hangModel 挂死模型：Generate 阻塞到 ctx 结束（模拟 TLS 黑洞、网关半开、
// 流式静默断流），并记录每次调用拿到的 deadline 余量（-1=无 deadline）。
// （resilient_test.go 另有 blockingModel——那边只测取消语义，这里还要断言
// 每次尝试的独立限时，记录面不同，故不共用。）
type hangModel struct {
	calls   int
	remains []time.Duration
}

func (b *hangModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if d, ok := ctx.Deadline(); ok {
		b.remains = append(b.remains, time.Until(d))
	} else {
		b.remains = append(b.remains, -1)
	}
	b.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *hangModel) Stream(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestClientAttemptTimeoutLimitsEachAttempt（llm#84）：单客户端路径的单次
// 尝试独立限时——挂死 provider 由 AttemptTimeout 兜底，且每次尝试各自计时
// （此前 attemptTimeout 恒 0、attemptCtx 原样透传，首次尝试吞掉全部时间
// 预算后 MaxRetries 沦为摆设——正是 retryPolicy 注释自己指出的那个坑）。
func TestClientAttemptTimeoutLimitsEachAttempt(t *testing.T) {
	m := &hangModel{}
	c := NewClient(m, "m", nil)
	c.MaxRetries = 2
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 2 * time.Millisecond
	c.AttemptTimeout = 40 * time.Millisecond

	start := time.Now()
	_, err := c.Generate(context.Background(), "T", msgs("hi"))
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("挂死 provider 应以尝试超时收场: %v", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("两次尝试应各 ~40ms 收口，实际 %v（重试沦为摆设？）", elapsed)
	}
	if m.calls != 2 {
		t.Fatalf("尝试数 = %d，应为 2（超时可重试，重试同样受限时约束）", m.calls)
	}
	for i, r := range m.remains {
		if r < 25*time.Millisecond || r > 45*time.Millisecond {
			t.Fatalf("第 %d 次尝试的独立限时 = %v，应 ≈40ms（每次尝试各自计时）", i+1, r)
		}
	}
}

// TestClientAttemptTimeoutZeroKeepsPassthrough 0=不限（向后兼容缺省）：无
// 独立限时时唯一挂钟约束来自外层 ctx——阻塞到外层 deadline 结束、ctx 错误
// 原样上抛，行为与接线前逐字一致。
func TestClientAttemptTimeoutZeroKeepsPassthrough(t *testing.T) {
	m := &hangModel{}
	c := NewClient(m, "m", nil)
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 2 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Generate(ctx, "T", msgs("hi"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("外层 ctx 超时应原样上抛: %v", err)
	}
	if time.Since(start) < 55*time.Millisecond {
		t.Fatal("0=不限：应阻塞到外层 ctx 结束，而非提前掐断")
	}
	if m.calls != 1 || m.remains[0] < 55*time.Millisecond {
		t.Fatalf("无独立限时应透传外层 ctx（唯一 deadline 是外层 ~60ms，不得有更短的派生限时）: calls=%d remains=%v", m.calls, m.remains)
	}
}
