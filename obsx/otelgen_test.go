// otelgen_test.go — generation span 六态钉测（oagentkit/llm#216）。
//
// 判据面是「Langfuse 端能读到什么」：断言全部落在 span 属性上
// （langfuse.observation.* / gen_ai.*），改名即测试红——属性名是检测面契约。
package obsx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// 全局 provider 只允许装一次（OTel 的 setDelegate 是一次性语义：第二次
// SetTracerProvider 被忽略）⇒ 整套测试共用一个 recorder，用例间用游标切窗口。
var (
	testRec *tracetest.SpanRecorder
	testTP  *sdktrace.TracerProvider
)

func TestMain(m *testing.M) {
	testRec = tracetest.NewSpanRecorder()
	testTP = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(testRec))
	otel.SetTracerProvider(testTP)
	code := m.Run()
	_ = testTP.Shutdown(context.Background())
	os.Exit(code)
}

type spanWindow struct{ from int }

func newSpanWindow(t *testing.T) *spanWindow {
	t.Helper()
	return &spanWindow{from: len(testRec.Ended())}
}

func (w *spanWindow) ended() []sdktrace.ReadOnlySpan {
	all := testRec.Ended()
	if len(all) <= w.from {
		return nil
	}
	return all[w.from:]
}

func chatInfo() *callbacks.RunInfo {
	return &callbacks.RunInfo{Name: "inner", Type: "OpenAI", Component: components.ComponentOfChatModel}
}

func attrOf(sp sdktrace.ReadOnlySpan, key string) (any, bool) {
	for _, kv := range sp.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsInterface(), true
		}
	}
	return nil, false
}

func wantAttr(t *testing.T, sp sdktrace.ReadOnlySpan, key, want string) {
	t.Helper()
	v, ok := attrOf(sp, key)
	if !ok {
		t.Fatalf("属性缺失: %s（实际属性: %v）", key, sp.Attributes())
	}
	if got := v.(string); got != want {
		t.Fatalf("属性 %s = %q，想要 %q", key, got, want)
	}
}

func wantNoAttr(t *testing.T, sp sdktrace.ReadOnlySpan, key string) {
	t.Helper()
	if v, ok := attrOf(sp, key); ok {
		t.Fatalf("属性 %s 不该出现，实际 = %v", key, v)
	}
}

func plainSanitize(s string) string { return strings.ReplaceAll(s, "hunter2", "****") }

// testCtx + testLabels：归因由宿主注入（本包不持有宿主 ctx 键）；这里直接给一份
// 固定标签，验的是「Labels 契约 → span 属性」的映射，而不是宿主注入机制。
func testCtx() context.Context { return context.Background() }

func testLabels() map[string]string {
	return map[string]string{
		"session_id": "sess-1", "case_id": "case-1", "agent": "argus", "model": "gpt-test",
	}
}

// TestOTelGenerationNonStream 非流式：模型/token/输入输出/归因全落。
func TestOTelGenerationNonStream(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{
		CaptureContent: true, MaxChars: 4000, Sanitize: plainSanitize,
		Labels: func(context.Context) map[string]string { return testLabels() }, AttributePrefix: "bq", SystemName: "oagentkit-test",
		ModelName: func(context.Context) string { return "gpt-test" },
	})

	in := &model.CallbackInput{
		Messages: []*schema.Message{
			{Role: schema.User, Content: "帮我排查内存告警"},
			{Role: schema.System, Content: "你是运维专家"},
		},
	}
	ctx := h.OnStart(testCtx(), chatInfo(), in)
	h.OnEnd(ctx, chatInfo(), &model.CallbackOutput{
		Message: &schema.Message{
			Role: schema.Assistant, Content: "先看 dmesg",
			ReasoningContent: "需要内存证据",
			ResponseMeta:     &schema.ResponseMeta{FinishReason: "stop"},
		},
		TokenUsage: &model.TokenUsage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150},
	})

	spans := rec.ended()
	if len(spans) != 1 {
		t.Fatalf("span 数 = %d，想要 1", len(spans))
	}
	sp := spans[0]
	if sp.Name() != "chat gpt-test" {
		t.Fatalf("span 名 = %q", sp.Name())
	}
	wantAttr(t, sp, attrLFType, "generation")
	wantAttr(t, sp, attrGenAIModel, "gpt-test")
	wantAttr(t, sp, attrLFModel, "gpt-test")
	wantAttr(t, sp, attrGenAISys, "oagentkit-test")
	wantAttr(t, sp, attrLFSession, "sess-1")
	wantAttr(t, sp, "bq.session_id", "sess-1")
	wantAttr(t, sp, "bq.case_id", "case-1")
	wantAttr(t, sp, "bq.agent", "argus")
	if v, _ := attrOf(sp, attrGenAIInTok); v.(int64) != 120 {
		t.Fatalf("input tokens = %v", v)
	}
	if v, _ := attrOf(sp, attrGenAIOutTok); v.(int64) != 30 {
		t.Fatalf("output tokens = %v", v)
	}
	inJSON, _ := attrOf(sp, attrLFInput)
	if !strings.Contains(inJSON.(string), "排查内存告警") {
		t.Fatalf("输入正文缺失: %v", inJSON)
	}
	outJSON, _ := attrOf(sp, attrLFOutput)
	if !strings.Contains(outJSON.(string), "先看 dmesg") || !strings.Contains(outJSON.(string), "需要内存证据") {
		t.Fatalf("输出正文缺失: %v", outJSON)
	}
	if v, ok := attrOf(sp, attrLFUsage); !ok || !strings.Contains(v.(string), `"total":150`) {
		t.Fatalf("usage_details = %v", v)
	}
}

// TestOTelGenerationContentOff 关采集：正文不落，但长度与元数据仍落。
func TestOTelGenerationContentOff(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{Sanitize: plainSanitize, ModelName: func(context.Context) string { return "gpt-test" }})

	in := &model.CallbackInput{Messages: []*schema.Message{{Role: schema.User, Content: "secret prompt"}}}
	ctx := h.OnStart(testCtx(), chatInfo(), in)
	h.OnEnd(ctx, chatInfo(), &model.CallbackOutput{
		Message:    &schema.Message{Content: "secret answer", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
		TokenUsage: &model.TokenUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	})

	sp := rec.ended()[0]
	wantNoAttr(t, sp, attrLFInput)
	wantNoAttr(t, sp, attrLFOutput)
	if v, ok := attrOf(sp, attrLFInputLen); !ok || v.(int64) != int64(len("secret prompt")) {
		t.Fatalf("input_length = %v（ok=%v）", v, ok)
	}
	if v, ok := attrOf(sp, attrLFOutputLen); !ok || v.(int64) != int64(len("secret answer")) {
		t.Fatalf("output_length = %v（ok=%v）", v, ok)
	}
	wantAttr(t, sp, attrGenAIModel, "gpt-test")
}

// TestOTelGenerationNoSanitize 开了采集但没给脱敏函数：按不采处理（绝不裸奔）。
func TestOTelGenerationNoSanitize(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{CaptureContent: true})
	ctx := h.OnStart(testCtx(), chatInfo(), &model.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: "secret prompt"}},
	})
	h.OnEnd(ctx, chatInfo(), &model.CallbackOutput{Message: &schema.Message{Content: "answer"}})

	sp := rec.ended()[0]
	wantNoAttr(t, sp, attrLFInput)
	wantNoAttr(t, sp, attrLFOutput)
}

// TestOTelGenerationStream 流式：异步排空副本流，累计 delta 得全文 + 末帧 usage。
func TestOTelGenerationStream(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{CaptureContent: true, Sanitize: plainSanitize})

	ctx := h.OnStart(testCtx(), chatInfo(), &model.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: "hi"}},
	})
	chunks := []callbacks.CallbackOutput{
		&model.CallbackOutput{Message: &schema.Message{Content: "内存"}},
		&model.CallbackOutput{Message: &schema.Message{Content: "告警", ReasoningContent: "推理"}},
		&model.CallbackOutput{
			Message:    &schema.Message{Content: "已定位", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
			TokenUsage: &model.TokenUsage{PromptTokens: 9, CompletionTokens: 5, TotalTokens: 14},
		},
	}
	start := time.Now()
	h.OnEndWithStreamOutput(ctx, chatInfo(), schema.StreamReaderFromArray(chunks))
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("OnEndWithStreamOutput 阻塞了 %s（必须异步排空）", elapsed)
	}

	deadline := time.Now().Add(3 * time.Second)
	var spans []sdktrace.ReadOnlySpan
	for time.Now().Before(deadline) {
		if spans = rec.ended(); len(spans) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(spans) == 0 {
		t.Fatal("流式 span 未收口")
	}
	sp := spans[0]
	outJSON, _ := attrOf(sp, attrLFOutput)
	if !strings.Contains(outJSON.(string), "内存告警已定位") || !strings.Contains(outJSON.(string), "推理") {
		t.Fatalf("流式正文未累计全文: %v", outJSON)
	}
	if v, _ := attrOf(sp, attrGenAIOutTok); v.(int64) != 5 {
		t.Fatalf("流式 output tokens = %v", v)
	}
	if v, ok := attrOf(sp, attrGenAIFinish); !ok || len(v.([]string)) != 1 || v.([]string)[0] != "stop" {
		t.Fatalf("finish_reasons = %v", v)
	}
}

// TestOTelGenerationError 错误：ERROR 级 + status message + span status。
func TestOTelGenerationError(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{CaptureContent: true, Sanitize: plainSanitize})
	ctx := h.OnStart(testCtx(), chatInfo(), &model.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: "hi"}},
	})
	h.OnError(ctx, chatInfo(), errors.New("upstream 502 bad gateway"))

	sp := rec.ended()[0]
	wantAttr(t, sp, attrLFLevel, "ERROR")
	if v, ok := attrOf(sp, attrLFStatus); !ok || !strings.Contains(v.(string), "502") {
		t.Fatalf("status_message = %v（ok=%v）", v, ok)
	}
}

// TestOTelGenerationRedact 永久留存面纪律：Sanitize 注入生效。
func TestOTelGenerationRedact(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{CaptureContent: true, Sanitize: plainSanitize})
	text := "手机号 13800138000，password=hunter2"
	ctx := h.OnStart(testCtx(), chatInfo(), &model.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: text}},
	})
	h.OnEnd(ctx, chatInfo(), &model.CallbackOutput{Message: &schema.Message{Content: text}})

	sp := rec.ended()[0]
	inJSON, _ := attrOf(sp, attrLFInput)
	if strings.Contains(inJSON.(string), "hunter2") {
		t.Fatalf("脱敏未生效: %s", inJSON)
	}
}

// TestOTelGenerationTruncate 超限截断留痕（且输入 JSON 仍合法）。
func TestOTelGenerationTruncate(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{CaptureContent: true, MaxChars: 40, Sanitize: plainSanitize})
	long := strings.Repeat("长文本", 200) // 600 rune >> 单条下限 160
	ctx := h.OnStart(testCtx(), chatInfo(), &model.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: long}},
	})
	h.OnEnd(ctx, chatInfo(), &model.CallbackOutput{Message: &schema.Message{Content: long}})

	sp := rec.ended()[0]
	inJSON, _ := attrOf(sp, attrLFInput)
	if !strings.Contains(inJSON.(string), "truncated") {
		t.Fatalf("输入截断未留痕: %s", inJSON)
	}
	outJSON, _ := attrOf(sp, attrLFOutput)
	if !strings.Contains(outJSON.(string), "truncated") {
		t.Fatalf("输出截断未留痕: %s", outJSON)
	}
	var decoded []map[string]string
	if err := json.Unmarshal([]byte(inJSON.(string)), &decoded); err != nil {
		t.Fatalf("输入 JSON 被截断打断: %v (%s)", err, inJSON)
	}
}

// TestOTelGenerationIgnoresNonModel 组件过滤：Tool/Lambda 回调不产 generation span。
func TestOTelGenerationIgnoresNonModel(t *testing.T) {
	rec := newSpanWindow(t)
	h := NewOTelGenerationHandler(GenTraceOptions{CaptureContent: true, Sanitize: plainSanitize})
	info := &callbacks.RunInfo{Name: "tool", Type: "Tool", Component: "Tool"}
	ctx := h.OnStart(testCtx(), info, nil)
	if genStateFrom(ctx) != nil {
		t.Fatal("非 ChatModel 组件不该开 span")
	}
	h.OnEnd(ctx, info, nil)
	if n := len(rec.ended()); n != 0 {
		t.Fatalf("非 ChatModel 组件产出了 %d 个 span", n)
	}
}
