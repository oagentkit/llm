package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/llmtest"
)

// 桩统一走 llmtest（Model/ToolModel）——此前本文件手写 failoverStub/withToolsStub。

// TestFailoverGenerate 主模型失败 → 备模型接管；主模型健康 → 备模型零调用。
func TestFailoverGenerate(t *testing.T) {
	// 脚本序：第一次 429 失败 → 切备；第二次"恢复"（末条重复语义）→ 主模型直出。
	primary := &llmtest.Model{RepeatLast: true, StreamContent: "primary",
		Script: []llmtest.Resp{{Err: errors.New("429 rate limited")}, {Content: "primary"}}}
	fallback := &llmtest.Model{RepeatLast: true, StreamContent: "fallback", Script: []llmtest.Resp{{Content: "fallback"}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	var hooked [][2]string
	m.OnFailover = func(from, to, _ string) { hooked = append(hooked, [2]string{from, to}) }

	resp, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil || resp.Content != "fallback" {
		t.Fatalf("主模型失败应切备模型: %v %q", err, resp.Content)
	}
	if len(hooked) != 1 || hooked[0] != [2]string{"main", "backup"} {
		t.Fatalf("OnFailover 应记录切换: %v", hooked)
	}

	// 主模型恢复（脚本第二条）：不再切换。
	hooked = nil
	resp, err = m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil || resp.Content != "primary" {
		t.Fatalf("主模型健康应直接返回: %v %q", err, resp.Content)
	}
	if len(hooked) != 0 {
		t.Fatalf("健康路径不应触发切换: %v", hooked)
	}
}

// TestFailoverStream 首块前失败同样切备模型。
func TestFailoverStream(t *testing.T) {
	primary := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("conn refused")}}}
	fallback := &llmtest.Model{RepeatLast: true, StreamContent: "backup-stream", Script: []llmtest.Resp{{Content: "backup-stream"}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	sr, err := m.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("Stream 首块前失败应切备: %v", err)
	}
	msg, err := sr.Recv()
	if err != nil || msg.Content != "backup-stream" {
		t.Fatalf("备模型流内容不符: %v %q", err, msg.Content)
	}
}

// TestFailoverCtxCancelled 调用方取消/超时是调用语义：不切换，原样上抛。
func TestFailoverCtxCancelled(t *testing.T) {
	primary := &llmtest.Model{RepeatLast: true, StreamContent: "primary", Script: []llmtest.Resp{{Err: context.Canceled, Content: "primary"}}}
	fallback := &llmtest.Model{RepeatLast: true, StreamContent: "fallback", Script: []llmtest.Resp{{Content: "fallback"}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Generate(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应原样上抛: %v", err)
	}
}

// TestFailoverBothFail 主备皆失败：备模型错误如实上抛（不吞错）。
func TestFailoverBothFail(t *testing.T) {
	primary := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("boom")}}}
	fallback := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("backup also down")}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	_, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err == nil || err.Error() != "backup also down" {
		t.Fatalf("备模型失败应如实上抛: %v", err)
	}
}

// TestFailoverWithTools WithTools 派生后仍保持 failover 语义（ADK 绑工具路径）。
func TestFailoverWithTools(t *testing.T) {
	p := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("down")}}}}
	f := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: "fb"}}}}

	m := NewFailoverModel(ChainLink{Model: p, Name: "main"}, ChainLink{Model: f, Name: "backup"})
	w, err := m.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools 不应报错: %v", err)
	}
	resp, err := w.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil || resp.Content != "fb" {
		t.Fatalf("派生实例应保留 failover 语义: %v %q", err, resp.Content)
	}
}

// TestFailoverWithToolsUnsupported 主/备不支持工具绑定时如实报错。
func TestFailoverWithToolsUnsupported(t *testing.T) {
	m := NewFailoverModel(ChainLink{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{}}}, Name: "main"}, ChainLink{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{}}}, Name: "backup"})
	if _, err := m.WithTools(nil); err == nil {
		t.Fatal("非 ToolCallingChatModel 主/备应报错")
	}
}

// Generate 路径 typed-nil 防御（llm#63）：此前只查接口 nil——typed-nil
// （(*llmtest.Model)(nil) 装箱进接口）在接口方法内 nil 解引用 panic，而
// Stream 用 isNilModel 正常跳过（「流式好使、非流式炸」）。两路径同用
// isNilModel 后 Generate 也跳过并切备模型。
func TestFailoverGenerateTypedNilSkipped(t *testing.T) {
	var typedNil *llmtest.Model
	fm := NewFailoverModel(
		ChainLink{Model: typedNil, Name: "bad"},
		ChainLink{Model: &llmtest.Model{Script: []llmtest.Resp{{Content: "good"}}}, Name: "backup"},
	)
	resp, err := fm.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("typed-nil 应被跳过切备模型: %v", err)
	}
	if resp.Content != "good" {
		t.Fatalf("应来自备模型: %q", resp.Content)
	}
}

// TestFailoverTailNilKeepsPriorError（llm#85）：链尾 nil 模型的装配错误不得
// 掩盖前链真实错误——倒数第二节的 429 应保留在最终错误里，且 Generate 与
// Stream 同口径（此前 Generate 只报「链含 nil 模型」、Stream 落入「链为空」，
// 同一故障两个文案、且都不是真实根因）。
func TestFailoverTailNilKeepsPriorError(t *testing.T) {
	rateLimited := errors.New("429 rate limited")
	newChain := func() *FailoverModel {
		return NewFailoverModel(
			ChainLink{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: rateLimited}}}, Name: "real"},
			ChainLink{Model: nil, Name: "bad-tail"},
		)
	}
	fm := newChain()
	_, gerr := fm.Generate(context.Background(), msgs("hi"))
	if gerr == nil {
		t.Fatal("Generate 应报错")
	}
	if !errors.Is(gerr, rateLimited) {
		t.Fatalf("Generate 应保留前链真实 429 错误: %v", gerr)
	}
	if !strings.Contains(gerr.Error(), "nil 模型") {
		t.Fatalf("Generate 应并列保留 nil 链节装配信息: %v", gerr)
	}

	fm2 := newChain()
	_, serr := fm2.Stream(context.Background(), msgs("hi"))
	if serr == nil {
		t.Fatal("Stream 应报错")
	}
	if !errors.Is(serr, rateLimited) {
		t.Fatalf("Stream 应保留前链真实 429 错误: %v", serr)
	}
	if !strings.Contains(serr.Error(), "nil 模型") {
		t.Fatalf("Stream 应与 Generate 同口径（nil 链节信息并列）: %v", serr)
	}
}

// TestFailoverAllNilChainErrorCopy（llm#85）：全 nil 链 Generate/Stream 返回
// 同一装配错误文案——此前 Stream 落入误导性的「failover 链为空」。
func TestFailoverAllNilChainErrorCopy(t *testing.T) {
	var typedNil *llmtest.Model
	fm := NewFailoverModel(ChainLink{Model: typedNil, Name: "a"}, ChainLink{Model: nil, Name: "b"})
	_, gerr := fm.Generate(context.Background(), msgs("hi"))
	_, serr := fm.Stream(context.Background(), msgs("hi"))
	if gerr == nil || serr == nil {
		t.Fatalf("全 nil 链应报错: %v / %v", gerr, serr)
	}
	if gerr.Error() != serr.Error() {
		t.Fatalf("Generate/Stream 错误文案应一致: %q vs %q", gerr, serr)
	}
	if !strings.Contains(gerr.Error(), "nil 模型") {
		t.Fatalf("应指向 nil 链节装配问题: %v", gerr)
	}
}

// checkerModel 模拟 eino-ext 回调契约的桩：实现 components.Checker 且启用
// （真实 eino-ext 模型即此形态——compose 据此跳过节点级回调注入）。
type checkerModel struct{ *llmtest.Model }

func (c *checkerModel) IsCallbacksEnabled() bool { return true }

// TestFailoverIsCallbacksEnabled 回归（oagentkit/llm#86）：链上任一链节启用
// 回调即 true——否则启用链节的自燃回调与 compose 节点级注入双发，一次调用
// 产出两条 usage 记录（预算/成本双计）。全部链节未实现 Checker 时如实返回
// false，由框架注入节点级回调兜底（此时无双计风险）。
func TestFailoverIsCallbacksEnabled(t *testing.T) {
	plain := &llmtest.Model{Script: []llmtest.Resp{{Content: "ok"}}}
	if components.IsCallbacksEnabled(NewFailoverModel(ChainLink{Model: plain, Name: "a"})) {
		t.Fatal("链节未实现 Checker 时应返回 false（框架兜底注入）")
	}

	enabled := &checkerModel{Model: &llmtest.Model{Script: []llmtest.Resp{{Content: "ok"}}}}
	fm := NewFailoverModel(ChainLink{Model: plain, Name: "a"}, ChainLink{Model: enabled, Name: "b"})
	if !components.IsCallbacksEnabled(fm) {
		t.Fatal("任一链节启用回调即应返回 true")
	}
}

// selfEmitCheckerModel 完整模拟 eino-ext 回调契约（oagentkit/llm#86 复现形态）：
// 实现 components.Checker 且在组件内部自燃 OnStart/OnEnd（acl/openai 每 chunk
// sw.Send(&model.CallbackOutput{...}) 即此路径）。裸桩进 compose 图只走自燃
// 一条路；不实现 Checker 的装饰器会让 compose 再注入一轮节点级回调 → 双计。
type selfEmitCheckerModel struct {
	mu    sync.Mutex
	calls int
}

func (m *selfEmitCheckerModel) IsCallbacksEnabled() bool { return true }

func (m *selfEmitCheckerModel) Generate(ctx context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	ctx = callbacks.EnsureRunInfo(ctx, "StubOpenAI", components.ComponentOfChatModel)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: in})
	msg := &schema.Message{Role: schema.Assistant, Content: "ok",
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
			Usage:        &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}}
	callbacks.OnEnd(ctx, &model.CallbackOutput{Message: msg,
		TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}})
	return msg, nil
}

// Stream 单块流直通（本回归只走 Invoke 非流式路径，契约补齐用）。
func (m *selfEmitCheckerModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	sr, w := schema.Pipe[*schema.Message](1)
	_ = w.Send(msg, nil)
	w.Close()
	return sr, nil
}

// TestFailoverCheckerSingleUsageRecordInCompose 回归（oagentkit/llm#86）：
// 装饰模型进 compose 图后一次调用只许产出一条 usage 记录。此前 FailoverModel
// 未实现 eino Checker——compose 在节点外再注入一轮 OnEnd（输出为装饰器返回的
// Message，经 ResponseMeta.Usage 回退也成一条记录），NewUsageHandler 记两条，
// 消费方把记录灌进预算即按 2 倍烧。
func TestFailoverCheckerSingleUsageRecordInCompose(t *testing.T) {
	stub := &selfEmitCheckerModel{}
	fm := NewFailoverModel(ChainLink{Model: stub, Name: "stub"})

	var mu sync.Mutex
	var recs []UsageRecord
	h := NewUsageHandler(func(r UsageRecord) {
		mu.Lock()
		recs = append(recs, r)
		mu.Unlock()
	})
	ctx := callbacks.InitCallbacks(context.Background(), nil, h)

	g := compose.NewGraph[[]*schema.Message, *schema.Message]()
	if err := g.AddChatModelNode("m", fm); err != nil {
		t.Fatalf("AddChatModelNode: %v", err)
	}
	if err := g.AddEdge(compose.START, "m"); err != nil {
		t.Fatalf("AddEdge start: %v", err)
	}
	if err := g.AddEdge("m", compose.END); err != nil {
		t.Fatalf("AddEdge end: %v", err)
	}
	r, err := g.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := r.Invoke(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil || out.Content != "ok" {
		t.Fatalf("Invoke: %v %q", err, out.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(recs) != 1 {
		t.Fatalf("compose 图内一次调用应只产 1 条 usage 记录（Checker 防节点级注入双计），got %d: %+v", len(recs), recs)
	}
	// 修复后 compose 不再注入节点级回调，唯一记录来自内层自燃（RunInfo 为
	// compose 注入的节点信息，Type 归因装饰器——图内组件自燃的标准归属）；
	// 未修复时第二条来自节点级注入、归因相同但 usage 重复。
	if recs[0].PromptTokens != 10 || recs[0].CompletionTokens != 5 || recs[0].TotalTokens != 15 {
		t.Fatalf("记录应携带内层自燃回调的 usage: %+v", recs[0])
	}
}
