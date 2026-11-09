package llm

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/llmtest"
)

func TestWithUsageLabelsCopies(t *testing.T) {
	src := map[string]string{"session": "s1"}
	ctx := WithUsageLabels(context.Background(), src)
	src["session"] = "mutated"
	got := labelsFrom(ctx)
	if got["session"] != "s1" {
		t.Fatalf("Labels 应拷贝, got %v", got)
	}
	// 空 map 不注入
	if labelsFrom(WithUsageLabels(context.Background(), nil)) != nil {
		t.Fatal("空 labels 不应注入")
	}
}

func TestUsageHandlerRecords(t *testing.T) {
	var got []UsageRecord
	h := NewUsageHandler(func(r UsageRecord) { got = append(got, r) })

	ctx := context.Background()
	ctx = WithUsageLabels(ctx, map[string]string{"agent": "test-agent", "session": "s-1"})
	ctx = WithCallCounter(ctx)

	// 模拟一次 ChatModel OnStart/OnEnd
	ctx = h.OnStart(ctx, &callbacks.RunInfo{Type: "OpenAI", Name: "chat"}, nil)

	msg := schema.Message{
		Role: schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
			Usage: &schema.TokenUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		},
	}
	// 用 model 的回调输出形态
	cbOut := model.ConvCallbackOutput(&model.CallbackOutput{
		Message: &msg,
		TokenUsage: &model.TokenUsage{
			PromptTokens:            100,
			PromptTokenDetails:      model.PromptTokenDetails{CachedTokens: 20},
			CompletionTokens:        50,
			TotalTokens:             150,
			CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: 10},
		},
	})
	// ConvCallbackOutput 接受 CallbackOutput；直接构造
	_ = cbOut

	h.OnEnd(ctx, &callbacks.RunInfo{Type: "OpenAI", Name: "chat"}, &model.CallbackOutput{
		Message: &msg,
		TokenUsage: &model.TokenUsage{
			PromptTokens:            100,
			PromptTokenDetails:      model.PromptTokenDetails{CachedTokens: 20},
			CompletionTokens:        50,
			TotalTokens:             150,
			CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: 10},
		},
	})

	if len(got) != 1 {
		t.Fatalf("应产 1 条记录, got %d", len(got))
	}
	r := got[0]
	if r.Model != "OpenAI" || r.PromptTokens != 100 || r.CachedTokens != 20 ||
		r.CompletionTokens != 50 || r.ReasoningTokens != 10 || r.TotalTokens != 150 {
		t.Fatalf("tokens 不符: %+v", r)
	}
	if r.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q", r.FinishReason)
	}
	if r.Iteration != 1 {
		t.Fatalf("Iteration = %d", r.Iteration)
	}
	if r.Labels["agent"] != "test-agent" || r.Labels["session"] != "s-1" {
		t.Fatalf("Labels = %v", r.Labels)
	}
	if r.DurationMS < 0 {
		t.Fatalf("DurationMS 不应为负: %d", r.DurationMS)
	}

	// 第二次调用 iteration 递增
	h.OnStart(ctx, &callbacks.RunInfo{Type: "OpenAI"}, nil)
	h.OnEnd(ctx, &callbacks.RunInfo{Type: "OpenAI"}, &model.CallbackOutput{
		TokenUsage: &model.TokenUsage{PromptTokens: 1, TotalTokens: 1},
	})
	if len(got) != 2 || got[1].Iteration != 2 {
		t.Fatalf("第二次 iteration 应为 2: %+v", got)
	}
}

func TestUsageHandlerSkips(t *testing.T) {
	var n int
	h := NewUsageHandler(func(UsageRecord) { n++ })
	ctx := context.Background()

	// 无 RunInfo
	h.OnEnd(ctx, nil, &model.CallbackOutput{
		TokenUsage: &model.TokenUsage{PromptTokens: 1, TotalTokens: 1},
	})
	// 无 usage
	h.OnEnd(ctx, &callbacks.RunInfo{Type: "OpenAI"}, &model.CallbackOutput{})
	// nil output
	h.OnEnd(ctx, &callbacks.RunInfo{Type: "OpenAI"}, nil)

	if n != 0 {
		t.Fatalf("应全部跳过, got %d", n)
	}
}

func TestUsageHandlerClientAccountedSkips(t *testing.T) {
	// 防重护栏（v0.10.12 自 obsx 移入并归位于记账侧）：Client 侧已配置
	// OnUsage/Budget 时 generateRetry 打标记，NewUsageHandler 对同一次物理
	// 调用跳过——两侧不双倍记账。
	var n int
	h := NewUsageHandler(func(UsageRecord) { n++ })
	ctx := withClientAccounting(context.Background())
	h.OnEnd(ctx, &callbacks.RunInfo{Type: "OpenAI"},
		&model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 10}})
	if n != 0 {
		t.Fatalf("Client 记账标记存在时应跳过: %d", n)
	}
	// 无标记时照常发射。
	h.OnEnd(context.Background(), &callbacks.RunInfo{Type: "OpenAI"},
		&model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 10}})
	if n != 1 {
		t.Fatalf("无标记应发射: %d", n)
	}
}

// TestUsageHandlerNestedScopeStartRefresh 嵌套作用域回归（llm#115）：handler
// 挂在根/全局时，agent ReAct 循环第 N 次模型调用的 OnStart 必须刷新起点——
// DurationMS 以最近一次调用起点起算，不得从根 OnStart 起算（随轮次单调虚涨：
// 第 N 次 ≈ 累计时长）。装饰器链同一次调用双触发 OnStart（原「已有起点不
// 覆盖」防重的目标场景）两次几乎同时，取后者无碍，一并钉住。
// 时序余量：根起点起算 ≥ 70+35/140ms，最近起点 ≈ 35ms——断言 < 90ms 两侧
// 均有充足余量，不因调度抖动翻红。
func TestUsageHandlerNestedScopeStartRefresh(t *testing.T) {
	var recs []UsageRecord
	h := NewUsageHandler(func(r UsageRecord) { recs = append(recs, r) })
	rootInfo := &callbacks.RunInfo{Type: "agent", Component: "agent"}
	callInfo := &callbacks.RunInfo{Type: "OpenAI", Component: "ChatModel"}
	usageOut := func() *model.CallbackOutput {
		return &model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 1, TotalTokens: 1}}
	}

	// 根节点 OnStart（handler 挂全局时 eino 对每个节点都触发）
	ctx := h.OnStart(context.Background(), rootInfo, nil)
	time.Sleep(70 * time.Millisecond)

	// 第 1 次模型调用：起点应刷新为本次 OnStart，DurationMS ≈ 35ms，
	// 不得继承根起点（≥ 105ms）
	mctx := h.OnStart(ctx, callInfo, nil)
	time.Sleep(35 * time.Millisecond)
	h.OnEnd(mctx, callInfo, usageOut())
	if len(recs) != 1 {
		t.Fatalf("应产 1 条记录, got %d", len(recs))
	}
	if recs[0].DurationMS >= 90 {
		t.Fatalf("第 1 次调用 DurationMS=%d 从根起点起算（llm#115）", recs[0].DurationMS)
	}

	// 第 2 次模型调用（ReAct 循环下一轮，ctx 仍从根派生）：
	// DurationMS 仍 ≈ 35ms，不得随轮次单调虚涨（从根起算 ≥ 140ms）
	mctx2 := h.OnStart(ctx, callInfo, nil)
	time.Sleep(35 * time.Millisecond)
	h.OnEnd(mctx2, callInfo, usageOut())
	if len(recs) != 2 {
		t.Fatalf("应产 2 条记录, got %d", len(recs))
	}
	if recs[1].DurationMS >= 90 {
		t.Fatalf("第 2 次调用 DurationMS=%d 单调虚涨（llm#115）", recs[1].DurationMS)
	}

	// 装饰器链同一次调用双触发 OnStart：取后者起点，时长仍按本次计
	mctx3 := h.OnStart(ctx, callInfo, nil)
	mctx3 = h.OnStart(mctx3, callInfo, nil)
	time.Sleep(35 * time.Millisecond)
	h.OnEnd(mctx3, callInfo, usageOut())
	if len(recs) != 3 {
		t.Fatalf("应产 3 条记录, got %d", len(recs))
	}
	if recs[2].DurationMS >= 90 {
		t.Fatalf("双触发 OnStart 时长应按本次计, got %d", recs[2].DurationMS)
	}
}

// TestUsageHandlerStreamBranch 流式分支回归（第十轮补覆盖）：usage 在中间
// 帧、其后有无 usage 的尾帧——记录仍产出（last-non-nil）；排空异步进行，
// handler 返回不被阻塞。
func TestUsageHandlerStreamBranch(t *testing.T) {
	// 构造三帧流：usage 帧、空帧、无 usage 尾帧
	sr, w := schema.Pipe[callbacks.CallbackOutput](3)
	go func() {
		w.Send(&model.CallbackOutput{
			Config:     &model.Config{Model: "m1"},
			TokenUsage: &model.TokenUsage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
		}, nil)
		w.Send(&model.CallbackOutput{}, nil)
		w.Send(&model.CallbackOutput{Config: &model.Config{Model: "m1"}}, nil)
		w.Close()
	}()
	done := make(chan struct{})
	ctx := callbacks.InitCallbacks(context.Background(),
		&callbacks.RunInfo{Type: "test", Component: components.ComponentOfChatModel},
		NewUsageHandler(func(r UsageRecord) {
			if r.PromptTokens == 11 {
				close(done)
			}
		}))
	// 触发流式时机：回交流消费一帧证明 handler 返回不被排空阻塞
	_, out := callbacks.OnEndWithStreamOutput(ctx, sr)
	_, _ = out.Recv()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("last-non-nil usage 应产出记录")
	}
}

// callbacksStubModel 会触发 eino callbacks 的模型桩：Generate 内分发 handler
// 的 OnStart/OnEnd（真实 provider 的 ChatModel 在 eino callbacks 装配下走同一
// 分发路径——llm#132 复现用；不经过 InitCallbacks，直接以 handler 为分发目标）。
type callbacksStubModel struct{ h callbacks.Handler }

func (m *callbacksStubModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	ctx = m.h.OnStart(ctx, &callbacks.RunInfo{Type: "OpenAI", Name: "chat"}, nil)
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "ok",
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
			Usage: &schema.TokenUsage{
				PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150,
			},
		},
	}
	m.h.OnEnd(ctx, &callbacks.RunInfo{Type: "OpenAI", Name: "chat"}, &model.CallbackOutput{
		Message: msg,
		TokenUsage: &model.TokenUsage{
			PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150,
		},
	})
	return msg, nil
}

func (m *callbacksStubModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

// TestClientOnUsageDetailedDedup_Issue132 复现 llm#132：只配 OnUsageDetailed
// （完整用量回调——需要 cached 的消费方用的出口）时，generateRetry 的「Client
// 已记账」防重标记不打 ⇒ callbacks 侧 NewUsageHandler 对同一次物理调用再记
// 一条 ⇒ 同一次调用两条用量（token/成本翻倍）。修复后 OnUsageDetailed 与
// OnUsage 同属 Client 侧遥测出口、同样参与防重。
func TestClientOnUsageDetailedDedup_Issue132(t *testing.T) {
	var handlerRecs []UsageRecord
	h := NewUsageHandler(func(r UsageRecord) { handlerRecs = append(handlerRecs, r) })

	var detailed []UsageRecord
	c := NewClient(&callbacksStubModel{h: h}, "gpt-4o", nil)
	c.OnUsageDetailed = func(stage string, rec UsageRecord) { detailed = append(detailed, rec) }

	if _, err := c.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	if len(detailed) != 1 {
		t.Fatalf("Client 侧 OnUsageDetailed 应记 1 条，got %d", len(detailed))
	}
	if len(handlerRecs) != 0 {
		t.Fatalf("只配 OnUsageDetailed 时 callbacks 侧应被防重标记跳过（llm#132：修复前同一次调用双计），got %d", len(handlerRecs))
	}
	if handlerRecs != nil {
		// 显式确认无记录（len 已断言，此处防 handler 误发空记录）。
		t.Fatalf("callbacks 侧不应产出任何记录: %+v", handlerRecs)
	}
}

// TestClientUsageOutletDedupMatrix_Issue132 防重矩阵钉约（llm#132）：
//
//	[A] 只配 OnUsage          → Client 侧 1 条，handler 跳过（既有契约）
//	[B] 只配 OnUsageDetailed  → Client 侧 1 条，handler 跳过（llm#132 修复点）
//	[C] 两个都配              → 两个 Client 侧出口各记 1 条（文档化「可同置」），handler 跳过
//	[D] 都不配                → handler 记 1 条（callbacks 侧是唯一出口）
func TestClientUsageOutletDedupMatrix_Issue132(t *testing.T) {
	run := func(usePlain, useDetailed bool) (plainN, detailedN, handlerN int) {
		h := NewUsageHandler(func(UsageRecord) { handlerN++ })

		c := NewClient(&callbacksStubModel{h: h}, "gpt-4o", nil)
		if usePlain {
			c.OnUsage = func(stage string, p, comp int) { plainN++ }
		}
		if useDetailed {
			c.OnUsageDetailed = func(stage string, rec UsageRecord) { detailedN++ }
		}
		if _, err := c.Generate(context.Background(), "R1", msgs("hi")); err != nil {
			t.Fatal(err)
		}
		return
	}
	cases := []struct {
		name                    string
		plain, detailed         bool
		wantPlain, wantDetailed int
	}{
		{"[A] 只配 OnUsage", true, false, 1, 0},
		{"[B] 只配 OnUsageDetailed", false, true, 0, 1},
		{"[C] 两个都配", true, true, 1, 1},
		{"[D] 都不配", false, false, 0, 0},
	}
	for _, tc := range cases {
		plainN, detailedN, handlerN := run(tc.plain, tc.detailed)
		if plainN != tc.wantPlain || detailedN != tc.wantDetailed {
			t.Errorf("%s: Client 侧 OnUsage=%d/OnUsageDetailed=%d，期望 %d/%d",
				tc.name, plainN, detailedN, tc.wantPlain, tc.wantDetailed)
		}
		wantHandler := 1
		if tc.plain || tc.detailed {
			wantHandler = 0 // 任一 Client 侧遥测出口配置即打防重标记
		}
		if handlerN != wantHandler {
			t.Errorf("%s: callbacks 侧 handler 应记 %d 条，got %d", tc.name, wantHandler, handlerN)
		}
	}
}

// TestGenerateRetryMarksAccountingForDetailed_Issue132 白盒钉约（llm#132）：
// generateRetry 的防重标记判定必须覆盖 OnUsageDetailed 单独配置的形态——
// 捕获传给模型的 ctx，断言标记已注入。
func TestGenerateRetryMarksAccountingForDetailed_Issue132(t *testing.T) {
	run := func(configure func(c *Client)) {
		t.Helper()
		cap := &ctxCaptureModel{Model: &llmtest.Model{Script: []llmtest.Resp{{Content: "ok"}}}}
		c := NewClient(cap, "gpt-4o", nil)
		configure(c)
		if _, err := c.Generate(context.Background(), "R1", msgs("hi")); err != nil {
			t.Fatal(err)
		}
		if !clientAccounted(cap.lastCtx) {
			t.Fatal("generateRetry 应打「Client 已记账」防重标记（OnUsageDetailed 也是遥测出口，llm#132）")
		}
	}
	run(func(c *Client) { c.OnUsage = func(string, int, int) {} })
	run(func(c *Client) { c.OnUsageDetailed = func(string, UsageRecord) {} })
	run(func(c *Client) {
		c.OnUsage = func(string, int, int) {}
		c.OnUsageDetailed = func(string, UsageRecord) {}
	})
	// 反证：两个出口都不配（callbacks 侧是唯一记账出口）时不打标记——
	// 否则 callbacks 侧用量遥测被静默吞掉。
	cap := &ctxCaptureModel{Model: &llmtest.Model{Script: []llmtest.Resp{{Content: "ok"}}}}
	c := NewClient(cap, "gpt-4o", nil)
	if _, err := c.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	if clientAccounted(cap.lastCtx) {
		t.Fatal("无任何 Client 侧遥测出口时不应打防重标记（Budget 不算遥测出口的既有口径同理）")
	}
}

// ctxCaptureModel 捕获最近一次 Generate 收到的 ctx（防重标记注入断言用）。
type ctxCaptureModel struct {
	*llmtest.Model
	lastCtx context.Context
}

func (m *ctxCaptureModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.lastCtx = ctx
	return m.Model.Generate(ctx, in, opts...)
}
