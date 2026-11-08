package llm

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
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
