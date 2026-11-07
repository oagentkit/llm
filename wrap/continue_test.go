package wrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// seqModel 按脚本逐次返回（耗尽后重复最后一条）。
type seqModel struct {
	mu     sync.Mutex
	resps  []*schema.Message
	errs   []error
	calls  int
	inputs [][]*schema.Message
}

func seqResp(content, finishReason string, toolCalls int) *schema.Message {
	m := &schema.Message{Role: schema.Assistant, Content: content}
	if fr := finishReason; fr != "" {
		m.ResponseMeta = &schema.ResponseMeta{FinishReason: fr}
	}
	for i := 0; i < toolCalls; i++ {
		m.ToolCalls = append(m.ToolCalls, schema.ToolCall{ID: "t1", Function: schema.FunctionCall{Name: "x"}})
	}
	return m
}

// seqRespUsage 带 usage 的响应（llm#82 记账合并断言用）。
func seqRespUsage(content, finishReason string, prompt, completion int) *schema.Message {
	m := seqResp(content, finishReason, 0)
	m.ResponseMeta.Usage = &schema.TokenUsage{
		PromptTokens: prompt, CompletionTokens: completion,
		TotalTokens: prompt + completion,
	}
	return m
}

func (s *seqModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs = append(s.inputs, input)
	i := s.calls
	if i >= len(s.resps) {
		i = len(s.resps) - 1
	}
	s.calls++
	if i < 0 {
		return nil, errors.New("seq: 空脚本")
	}
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	return s.resps[i], nil
}

func (s *seqModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("seq: stream 未实现")
}

func (s *seqModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return s, nil }

func (s *seqModel) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestContinueModelMergesTruncated(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqResp("part-", "length", 0),
		seqResp("rest", "stop", 0),
	}}
	var rounds []int
	cm := NewContinueModel(inner, "R35", func(stage string, round, added int) {
		rounds = append(rounds, round)
	})
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out.Content != "part-rest" {
		t.Fatalf("应顺序拼接，got %q", out.Content)
	}
	if out.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("finish reason 应取最新，got %q", out.ResponseMeta.FinishReason)
	}
	if len(rounds) != 1 || rounds[0] != 1 {
		t.Fatalf("续写轮次不符: %v", rounds)
	}
	if inner.callCount() != 2 {
		t.Fatalf("应恰好 2 次调用，got %d", inner.callCount())
	}
	// 续写请求应携带部分输出 + 续写指令
	second := inner.inputs[1]
	if second[len(second)-1].Role != schema.User || !strings.Contains(second[len(second)-1].Content, "截断处") {
		t.Fatal("续写指令缺失")
	}
	if second[len(second)-2].Content != "part-" {
		t.Fatal("部分输出未随续写请求回传")
	}
}

func TestContinueModelMaxRounds(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqResp("a", "length", 0),
		seqResp("b", "length", 0),
		seqResp("c", "length", 0),
		seqResp("d", "stop", 0),
	}}
	cm := NewContinueModel(inner, "R3", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out.Content != "abcd" || inner.callCount() != 4 {
		t.Fatalf("三轮续写后拼接 %q（%d 次调用）", out.Content, inner.callCount())
	}
}

func TestContinueModelToolCallsNotContinued(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqResp("", "length", 2), // 带工具调用的截断：不续写
	}}
	cm := NewContinueModel(inner, "chat", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.ToolCalls) != 2 || inner.callCount() != 1 {
		t.Fatal("带工具调用的截断消息不得续写")
	}
}

func TestContinueModelErrorKeepsPartial(t *testing.T) {
	inner := &seqModel{
		resps: []*schema.Message{
			seqResp("partial", "length", 0),
			seqResp("", "", 0),
		},
		errs: []error{nil, errors.New("续写炸了")},
	}
	cm := NewContinueModel(inner, "qa", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("续写失败应返回部分而非报错: %v", err)
	}
	if out.Content != "partial" {
		t.Fatalf("应保留部分输出，got %q", out.Content)
	}
}

func TestContinueModelPassthroughNormal(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{seqResp("done", "stop", 0)}}
	cm := NewContinueModel(inner, "router", nil)
	out, _ := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if out.Content != "done" || inner.callCount() != 1 {
		t.Fatal("正常结束不应触发续写")
	}
}

// TestContinueModelDerivedDeadlineNotSwallowed（llm#77，锁 #69 修复的契约）：
// 续写轮撞上内层派生 deadline（Client 的 AttemptTimeout、http.Client.Timeout
// 等产生的 context.DeadlineExceeded）而父 ctx 仍存活时——ctx.Err()==nil，
// 腰斩的半截输出不得以 (msg, nil) 的零错误形态当成功返回，必须携带触发
// 错误上抛（消费方拿到静默残缺内容是评审结论/JSON 成品场景的 P1）。
func TestContinueModelDerivedDeadlineNotSwallowed(t *testing.T) {
	inner := &seqModel{
		// 第二条 resp 仅占位（seqModel 的 errs 索引随 resps 钳制，需撑开长度）
		resps: []*schema.Message{seqResp("partial-", "length", 0), seqResp("", "stop", 0)},
		// 续写轮返回包装过的派生 deadline（外层 ctx 健康）
		errs: []error{nil, fmt.Errorf("attempt timeout: %w", context.DeadlineExceeded)},
	}
	cm := NewContinueModel(inner, "R77", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err == nil {
		t.Fatal("派生 deadline 必须以错误上抛——(半截, nil) 会把腰斩输出当完整品")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误链应保留底层 DeadlineExceeded: %v", err)
	}
	if out == nil || out.Content != "partial-" {
		t.Fatalf("错误路径应同时携带半截输出供上层处置: %v", out)
	}
}

// TestContinueModelParentCancelPropagates 父 ctx 取消：ctx.Err() 原样上抛
// （调用方语义的失败不是「续写失败」，不得落入部分返回分支）。
func TestContinueModelParentCancelPropagates(t *testing.T) {
	inner := &seqModel{
		// 第二条 resp 仅占位（seqModel 的 errs 索引随 resps 钳制，需撑开长度）
		resps: []*schema.Message{seqResp("partial-", "length", 0), seqResp("", "stop", 0)},
		errs:  []error{nil, context.Canceled},
	}
	cm := NewContinueModel(inner, "R77", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := cm.Generate(ctx, []*schema.Message{bigTool(schema.User, 10)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("父 ctx 取消应原样上抛: %v", err)
	}
	if out == nil || out.Content != "partial-" {
		t.Fatalf("应同时携带半截输出: %v", out)
	}
}

// TestContinueModelMergesUsageAcrossRounds（llm#82）：续写合并不得以末轮
// meta 整体覆盖——首轮 usage 丢失会让按 ResponseMeta.Usage 记账的消费方
// 只计末轮，成本/预算口径系统性低估。prompt 取首轮、completion 求和、
// total 重算自洽、FinishReason 以末轮为准。
func TestContinueModelMergesUsageAcrossRounds(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqRespUsage("part-", "length", 100, 500), // 首轮：输出大头
		seqRespUsage("rest", "stop", 220, 80),     // 续写轮：prompt 含回传的部分输出
	}}
	cm := NewContinueModel(inner, "cost", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	u := out.ResponseMeta.Usage
	if u.PromptTokens != 100 {
		t.Fatalf("prompt 应取首轮（整段上下文按一份计）: %d", u.PromptTokens)
	}
	if u.CompletionTokens != 580 {
		t.Fatalf("completion 应逐轮累加 500+80: %d", u.CompletionTokens)
	}
	if u.TotalTokens != 680 {
		t.Fatalf("total 应重算为 prompt+completion: %d", u.TotalTokens)
	}
	if out.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("finish reason 以末轮为准: %q", out.ResponseMeta.FinishReason)
	}
}

// TestContinueModelUsageAccumulatesThreeRounds 三轮续写的累加跨轮保持
// （首轮 prompt 不被后续轮覆盖、completion 逐轮滚动求和）。
func TestContinueModelUsageAccumulatesThreeRounds(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqRespUsage("a", "length", 100, 100),
		seqRespUsage("b", "length", 150, 100),
		seqRespUsage("c", "stop", 300, 50),
	}}
	cm := NewContinueModel(inner, "cost", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	u := out.ResponseMeta.Usage
	if u.PromptTokens != 100 || u.CompletionTokens != 250 || u.TotalTokens != 350 {
		t.Fatalf("三轮累加口径不符: prompt=%d completion=%d total=%d", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
}

// TestContinueModelKeepsFirstUsageWhenLastMissing 末轮缺 usage（部分兼容端点
// 不回传）：保住首轮 usage 不丢账，FinishReason 仍以末轮为准。
func TestContinueModelKeepsFirstUsageWhenLastMissing(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqRespUsage("part-", "length", 100, 500),
		seqResp("rest", "stop", 0), // 末轮无 usage
	}}
	cm := NewContinueModel(inner, "cost", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	u := out.ResponseMeta.Usage
	if u == nil || u.PromptTokens != 100 || u.CompletionTokens != 500 {
		t.Fatalf("末轮缺 usage 应保住首轮 usage: %+v", u)
	}
	if out.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("finish reason 以末轮为准: %q", out.ResponseMeta.FinishReason)
	}
}
