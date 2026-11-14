package llm

// 2026-11-14 issue 清零战回归测试（#186-#197，#198 下沉提案不在本车道）：
// 每条用例钉一个失效形态，标题带 issue 号——回归时按号检索。
// #186/#191(port 侧) 的用例在 port 包，#196 在 llmtest 包，#187(wrap 侧) 在 wrap 包。

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"github.com/meguminnnnnnnnn/go-openai"
	"github.com/oagentkit/llm/llmtest"
	"github.com/oekit/pkg/httpjson"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- #187 fit 裁剪与记账同字段面（Content + ToolCalls + 多模态文本部件） ---

// TestMsgRuneCountCoversToolCallsAndMultiContent_Issue187 单源计数面：
// 大入参 ToolCalls 与多模态文本部件必须计入——与 estimateMsgTokens（#139）
// 成对，裁剪与记账两套出口同一口径。
func TestMsgRuneCountCoversToolCallsAndMultiContent_Issue187(t *testing.T) {
	args := `{"query":"` + strings.Repeat("v", 100) + `"}`
	m := &schema.Message{
		Role:      schema.Assistant,
		Content:   "",
		ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "search", Arguments: args}}},
	}
	want := utf8Len("search") + utf8Len(args)
	if got := MsgRuneCount(m); got != want {
		t.Fatalf("MsgRuneCount 应计入工具名+参数 JSON：got %d want %d", got, want)
	}
	mm := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: strings.Repeat("图", 50)}}}
	if got := MsgRuneCount(mm); got != 50 {
		t.Fatalf("多模态文本部件应计入: %d", got)
	}
	if MsgRuneCount(nil) != 0 {
		t.Fatal("nil 消息应为 0")
	}
}

func utf8Len(s string) int { return len([]rune(s)) }

// TestFitInputSeesToolCalls_Issue187 ToolCalls 占大头时 fit 必须触发：
// Content-only 口径下 total ≤ limit 直通（旧 bug），全字段口径下必须裁剪，
// 且 ToolCalls 本体不被改写（协议配对完整性优先）。
func TestFitInputSeesToolCalls_Issue187(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10} // limit = 121 rune
	user := strings.Repeat("你", 100)
	args := `{"k":"` + strings.Repeat("v", 31) + `"}`
	msgList := []*schema.Message{
		schema.UserMessage(user),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "f", Arguments: args}}}},
	}
	if err := c.fitInput(msgList); err != nil {
		t.Fatalf("user 截断后应入窗: %v", err)
	}
	if utf8Len(msgList[0].Content) >= 100 {
		t.Fatal("ToolCalls 占大头时 user 消息应被裁剪（旧口径只数 Content 会误判未超限直通）")
	}
	if !strings.Contains(msgList[0].Content, "已截断留痕") {
		t.Fatal("截断应留痕")
	}
	if msgList[1].ToolCalls[0].Function.Arguments != args {
		t.Fatal("ToolCalls 参数不得被改写")
	}
}

// --- #188 fitInput 无 user 兜底、回环裁剪、不可收敛给可诊断错误 ---

// TestFitInputWithoutUserFallsBackToTool_Issue188 纯 system/tool 会话超限：
// 此前 longest<0 一行不改静默直通；修复后回环截最长可裁正文。
func TestFitInputWithoutUserFallsBackToTool_Issue188(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10} // limit = 121 rune
	sys := "sys"
	tool := strings.Repeat("果", 200)
	msgList := []*schema.Message{
		schema.SystemMessage(sys),
		{Role: schema.Tool, Content: tool},
	}
	if err := c.fitInput(msgList); err != nil {
		t.Fatalf("tool 正文可裁，应收敛入窗: %v", err)
	}
	if utf8Len(msgList[1].Content) >= 200 {
		t.Fatal("无 user 会话应退化为截最长 tool 正文")
	}
	if msgList[0].Content != sys {
		t.Fatal("system 提示词不裁")
	}
	if MsgsRuneCount(msgList) > 121 {
		t.Fatalf("裁剪后应入窗: %d", MsgsRuneCount(msgList))
	}
}

// TestFitInputLoopsUntilFit_Issue188 超限量 > 最长 user 长度：此前只裁一条、
// 裁完不复查（user 削成注记、总量仍超限照发）；修复后回环迭代直至入窗。
func TestFitInputLoopsUntilFit_Issue188(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10} // limit = 121 rune
	msgList := []*schema.Message{
		schema.UserMessage(strings.Repeat("问", 200)),
		{Role: schema.Tool, Content: strings.Repeat("果", 200)},
	}
	if err := c.fitInput(msgList); err != nil {
		t.Fatalf("回环裁剪应入窗: %v", err)
	}
	if MsgsRuneCount(msgList) > 121 {
		t.Fatalf("回环后仍超限: %d", MsgsRuneCount(msgList))
	}
	if !strings.Contains(msgList[0].Content, "已截断留痕") ||
		!strings.Contains(msgList[1].Content, "已截断留痕") {
		t.Fatal("user 与 tool 都应被裁并留痕")
	}
}

// TestFitInputUnconvergedIsDiagnosable_Issue188 无可裁对象（仅 system）仍超限：
// 不再静默放行——返回可诊断错误，且被分类成确定性失败（不重试同模型，
// 降级链可切换更大窗口的备选）。
func TestFitInputUnconvergedIsDiagnosable_Issue188(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10}
	sys := strings.Repeat("系统", 250)
	msgList := []*schema.Message{schema.SystemMessage(sys)}
	err := c.fitInput(msgList)
	if err == nil {
		t.Fatal("无可裁对象且超限，应报错而非静默直通")
	}
	if !strings.Contains(err.Error(), "context too long") {
		t.Fatalf("错误应带 context too long marker: %v", err)
	}
	if ClassifyLLMError(err).Retryable {
		t.Fatal("本地可诊断的窗口超限应判确定性失败（不重试）")
	}
	if msgList[0].Content != sys {
		t.Fatal("system 提示词不得被裁")
	}
}

// --- #189 退避封顶档软上限 ---

// TestBackoffCappedTierStaysSpread_Issue189 封顶档并发打散：此前「先抖动后钳制」
// 把 ≥ceil 档位的等待削成同一毫秒（429 群撞复活）；软上限下必须保持散布。
func TestBackoffCappedTierStaysSpread_Issue189(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 64 {
		d := backoffDelay(6, 2*time.Second, 32*time.Second, true)
		if d < 32*time.Second || d >= 48*time.Second {
			t.Fatalf("封顶档应落在 [32s,48s): %v", d)
		}
		seen[d] = true
	}
	if len(seen) < 8 {
		t.Fatalf("封顶档应保持打散，64 个样本只出现 %d 个不同值", len(seen))
	}
}

// --- #190 IsTransientStreamError 显式白名单 ---

// TestIsTransientStreamErrorUnknownPermanent_Issue190 未知永久错误（无 marker
// 文案）不再缺省瞬态——此前最多重开流 10 次、每轮重烧 token 与延迟。
func TestIsTransientStreamErrorUnknownPermanent_Issue190(t *testing.T) {
	permanent := []error{
		errors.New("content policy violation"),
		errors.New("function calling is not supported by this model"),
		errors.New("model deprecated by provider"),
		errors.New("insufficient_quota: you have run out of credits"),
	}
	for _, e := range permanent {
		if IsTransientStreamError(e) {
			t.Errorf("未知永久错误不应判瞬态: %v", e)
		}
	}
}

// TestIsTransientStreamErrorStructured_Issue190 结构化瞬态类型：ctx 超时与
// 429/5xx 状态码瞬态；其余 4xx 确定性失败不重开流。
func TestIsTransientStreamErrorStructured_Issue190(t *testing.T) {
	transient := []error{
		fmt.Errorf("attempt: %w", context.DeadlineExceeded),
		&openai.RequestError{HTTPStatusCode: 429, Err: errors.New("rate limited")},
		&openai.RequestError{HTTPStatusCode: 503, Err: errors.New("upstream")},
		errors.New("HTTP 502"),
		errors.New("connection reset by peer"),
	}
	for _, e := range transient {
		if !IsTransientStreamError(e) {
			t.Errorf("应判瞬态: %v", e)
		}
	}
	permanent := []error{
		&openai.RequestError{HTTPStatusCode: 400, Err: errors.New("bad shape")},
		context.Canceled,
	}
	for _, e := range permanent {
		if IsTransientStreamError(e) {
			t.Errorf("不应判瞬态: %v", e)
		}
	}
}

// --- #191 失败面记账 ---

// TestClientFailurePathAccounting_Issue191 失败的物理尝试按估算记 prompt 侧：
// Budget/OnUsage/OnUsageDetailed 三出口同账，UsageRecord.Partial 留痕；
// 成功路径记账真实 usage，两路互斥不双计。
func TestClientFailurePathAccounting_Issue191(t *testing.T) {
	m := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Err: errors.New("500 internal error")},
		{Content: "ok", Prompt: 100, Completion: 50},
	}}
	budget := NewBudget(10000)
	var calls int
	var partials []bool
	c := NewClient(m, "test", budget)
	c.OnUsage = func(stage string, p, comp int) {
		calls++
		if calls == 1 && comp != 0 {
			t.Fatalf("失败面记账 completion 应为 0: %d", comp)
		}
	}
	c.OnUsageDetailed = func(stage string, rec UsageRecord) {
		partials = append(partials, rec.Partial)
	}
	if _, err := c.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("两次物理尝试都应记账，实际 %d", calls)
	}
	if len(partials) != 2 || !partials[0] || partials[1] {
		t.Fatalf("首笔应 Partial=true（失败面估算）、次笔 false（成功真实）: %v", partials)
	}
	est := estimateMsgTokens(msgs("hi"))
	if got := budget.Used(); got != est+150 {
		t.Fatalf("预算应 = 失败面估算 %d + 成功真实 150，实际 %d", est, got)
	}
}

// --- #192 Retry-After sink 生命周期 = 单次失败 ---

// captureTransport 记录每轮物理请求的 ctx（#192：断言每轮拿到的是全新 sink）。
type captureTransport struct {
	mu   sync.Mutex
	ctxs []context.Context
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.ctxs = append(t.ctxs, req.Context())
	n := len(t.ctxs)
	t.mu.Unlock()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		Request:    req,
	}
	if n == 1 {
		resp.Header.Set("Retry-After", "1") // 仅首轮带服务端建议
	}
	return resp, nil
}

// TestRetryAfterSinkLifecycle_Issue192 sink 每轮物理调用换新：首轮 429 的
// Retry-After 只影响首轮失败后的等待；次轮 429 不带头时本地曲线接管，
// 陈旧建议不得复用（旧实现次轮仍读到首轮旧窗口，最坏白等 Σhint）。
func TestRetryAfterSinkLifecycle_Issue192(t *testing.T) {
	capT := &captureTransport{}
	provider, err := NewOpenAIProvider(context.Background(), OpenAIProviderConfig{
		BaseURL: "http://llm-issue192.invalid", APIKey: "k", Model: "m",
		CaptureRetryAfter: true, HTTPClient: &http.Client{Transport: capT},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Model: provider.Model(), ModelName: "m", MaxRetries: 2,
		BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	_, err = c.Generate(context.Background(), "R1", msgs("hi"))
	if err == nil {
		t.Fatal("两次 429 应失败返回")
	}
	if len(capT.ctxs) != 2 {
		t.Fatalf("应两次物理尝试，实际 %d", len(capT.ctxs))
	}
	if got := RetryAfterFrom(capT.ctxs[0]); got != time.Second {
		t.Fatalf("首轮 sink 应捕获服务端建议 1s: %v", got)
	}
	if got := RetryAfterFrom(capT.ctxs[1]); got != 0 {
		t.Fatalf("次轮应为全新 sink（hint 生命周期 = 单次失败）: %v", got)
	}
}

// TestSelectRetryDelayClampSanity 取舍钳制面回归（陈旧/超钳制建议不采信）。
func TestSelectRetryDelayClampSanity(t *testing.T) {
	if got := SelectRetryDelay(4*time.Second, 300*time.Second); got != 300*time.Second {
		t.Fatalf("≤5min 的建议应被采信: %v", got)
	}
	if got := SelectRetryDelay(4*time.Second, httpjson.MaxServerRetryAfter+time.Second); got != 4*time.Second {
		t.Fatalf("超钳制建议应回落本地曲线: %v", got)
	}
}

// --- #194 截断收手在退避等待之前 ---

// TestTruncatedGiveUpBeforeBackoff_Issue194 提升后仍截断：下一轮收手 break
// 必须排在退避等待之前——旧实现白等一个完整退避才放弃（可观测为第 3 次调用）。
func TestTruncatedGiveUpBeforeBackoff_Issue194(t *testing.T) {
	m := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Content: "半截", FinishReason: "length"},
	}}
	c := NewClient(m, "test", nil)
	c.BaseDelay = time.Second // 若收手判断排在等待之后，第 3 轮前会白等 1s
	c.MaxDelay = time.Second
	c.MaxOutputTokens = 100

	_, err := c.Generate(context.Background(), "R1", msgs("hi"))
	if err == nil {
		t.Fatal("提升后仍截断应失败返回")
	}
	if m.Calls != 2 {
		t.Fatalf("首次截断 + 提升轮仍截断即收手，应恰 2 次调用，实际 %d（收手晚于退避等待会多烧一轮）", m.Calls)
	}
}

// --- #193 StageRouter.Use("") 静默全局兜底 ---

// TestStageRouterUseRejectsEmptyStage_Issue193 Use("") panic（空串前缀恒命中
// 且长度 0——曾静默接管全部未命中阶段并压过构造期 def）；正常前缀/精确路由
// 与 def 兜底语义不受影响。
func TestStageRouterUseRejectsEmptyStage_Issue193(t *testing.T) {
	gen := NewClient(&llmtest.Model{RepeatLast: true}, "test", nil)
	sr := NewStageRouter(gen)
	assertPanic(t, func() { sr.Use("", gen) })
	// 正常注册与兜底不受影响
	sr.Use("R1", gen)
	if sr.pick("R1a") != gen || sr.pick("qa") != gen || sr.pick("other") != gen {
		t.Fatal("前缀/精确/兜底路由应不受影响")
	}
}

func assertPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("应 panic（构造期配置错误尽早暴露）")
		}
	}()
	fn()
}
