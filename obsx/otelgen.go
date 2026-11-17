// otelgen.go — LLM 调用级 OTel span 发射端（Langfuse / Jaeger 的 generation 视图）。
//
// # 为什么在这里
//
// obsx 是矩阵里 eino callbacks 的适配层：NewUsageHandler 管账本、
// NewTracingHandler 管日志。两者**都不产 OTel span** ⇒ 宿主把 oekit/tracing 的
// OTLP exporter 指向 Langfuse 时，trace 里只有宿主自己开的 span，**看不到每一次
// LLM 调用**（没有 prompt/completion、没有按模型维度、没有每次调用的 token）。
// Langfuse 的 generation 视图因此是空的。本文件补的就是这一层（oagentkit/llm#216）。
//
// # 属性词表（Langfuse 官方 OTel 摄取规范，2026-11-17 实读）
//
//   - langfuse.observation.type=generation
//   - langfuse.observation.input / output（JSON 字符串）
//   - langfuse.observation.model.name / gen_ai.request.model
//   - gen_ai.operation.name=chat / gen_ai.system=<宿主>
//   - gen_ai.usage.input_tokens / output_tokens
//   - langfuse.observation.usage_details（JSON：input/output/total/cached/reasoning）
//   - gen_ai.response.finish_reasons
//   - langfuse.session.id（归因标签含 session_id 时）
//
// 采集关闭时仍写 langfuse.observation.input_length/output_length（rune 计数）——
// 「没采正文」与「正文为空」必须可区分。
//
// # 流式纪律（勿踩）
//
// OnEndWithStreamOutput 必须**异步**排空副本流：eino 的 handler 分发是同步的，
// Stream() 在回调返回后才把流交给调用方——同步排空会把真流式阻塞成「全量生成完
// 才返回」（首分片延迟 = 全程生成时间）。副本流必须 Close（不关则整条管道泄漏）。
//
// # 正文采集是显式开关
//
// CaptureContent 缺省关：trace store 是新增的永久留存面。开启时必须同时提供
// Sanitize（宿主注入脱敏；本包不依赖宿主 PII 库）——没给 Sanitize 就按不采处理，
// 绝不让原文裸奔。正文按 MaxChars（缺省 4000 rune）逐条限宽，截断留痕。
package obsx

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/oekit/concurrency/async"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Langfuse / GenAI 语义约定属性名（检测面契约：改名即 Langfuse 端字段丢失）。
const (
	attrLFType       = "langfuse.observation.type"
	attrLFInput      = "langfuse.observation.input"
	attrLFOutput     = "langfuse.observation.output"
	attrLFModel      = "langfuse.observation.model.name"
	attrLFSession    = "langfuse.session.id"
	attrLFUsage      = "langfuse.observation.usage_details"
	attrLFLevel      = "langfuse.observation.level"
	attrLFStatus     = "langfuse.observation.status_message"
	attrLFInputLen   = "langfuse.observation.input_length"
	attrLFOutputLen  = "langfuse.observation.output_length"
	attrGenAIOp      = "gen_ai.operation.name"
	attrGenAISys     = "gen_ai.system"
	attrGenAIModel   = "gen_ai.request.model"
	attrGenAIInTok   = "gen_ai.usage.input_tokens"
	attrGenAIOutTok  = "gen_ai.usage.output_tokens"
	attrGenAIFinish  = "gen_ai.response.finish_reasons"
	labelKeySession  = "session_id"
	defaultGenMaxLen = 4000
	defaultAttrPfx   = "oagentkit"
	defaultSystem    = "oagentkit"
	tracerName       = "github.com/oagentkit/llm/obsx"
)

// genTracer 全局 tracer（provider 由宿主用 oekit/tracing 装配；otel 全局 provider
// 是延迟委托的 ⇒ 包初始化时取 tracer 也安全）。
var genTracer = otel.Tracer(tracerName)

// GenTraceOptions generation span 采集配置。
type GenTraceOptions struct {
	// CaptureContent 是否写 prompt/completion 正文（缺省 false）。
	// 为 true 时必须提供 Sanitize，否则按不采处理。
	CaptureContent bool
	// MaxChars 单条正文上限（rune；≤0 按 4000）。超限截断并在尾部留痕。
	MaxChars int
	// Sanitize 宿主注入的脱敏函数（本包不依赖任何宿主 PII 库）；nil=不采正文。
	Sanitize func(string) string
	// Labels 本次调用的归因标签（宿主自定键）。每个非空键写成 span 属性
	// "<AttributePrefix>.<key>"；key=session_id 时额外写 langfuse.session.id
	// （Langfuse 用它把多条 trace 聚成会话）。nil=不写归因属性。
	Labels func(context.Context) map[string]string
	// AttributePrefix 归因标签的 span 属性前缀（缺省 "oagentkit"）。
	AttributePrefix string
	// SystemName gen_ai.system 的值（缺省 "oagentkit"）。
	SystemName string
	// ModelName 可选的模型名解析（宿主归因面）：eino 回调的 in.Config.Model 缺失
	// 时用它。返回空 = 回落到组件标识（RunInfo.Type，如 "OpenAI"——多模型混跑
	// 无法区分，代理网关场景尤其如此）。宿主通常在这里回填「选链后实际模型名」。
	ModelName func(context.Context) string
}

func (o GenTraceOptions) maxChars() int {
	if o.MaxChars <= 0 {
		return defaultGenMaxLen
	}
	return o.MaxChars
}

func (o GenTraceOptions) prefix() string {
	if o.AttributePrefix == "" {
		return defaultAttrPfx
	}
	return o.AttributePrefix
}

func (o GenTraceOptions) system() string {
	if o.SystemName == "" {
		return defaultSystem
	}
	return o.SystemName
}

func (o GenTraceOptions) canCapture() bool { return o.CaptureContent && o.Sanitize != nil }

func (o GenTraceOptions) clean(s string) string {
	if o.Sanitize == nil {
		return s
	}
	return o.Sanitize(s)
}

// genSpanKey ctx 键：本次模型调用的 span 状态。
type genSpanKey struct{}

type genSpanState struct{ span trace.Span }

// NewOTelGenerationHandler 构建 eino callbacks.Handler（generation span 出口）。
//
// 与 NewUsageHandler/NewTracingHandler 并列注册即可（callbacks.InitCallbacks 的
// handler 列表）：三者出口不同（账本 / 日志 / 分布式追踪），互不替代。
func NewOTelGenerationHandler(opts GenTraceOptions) callbacks.Handler {
	h := &genHandler{opt: opts}
	return callbacks.NewHandlerBuilder().
		OnStartFn(h.onStart).
		OnEndFn(h.onEnd).
		OnErrorFn(h.onError).
		OnEndWithStreamOutputFn(h.onStreamEnd).
		Build()
}

type genHandler struct{ opt GenTraceOptions }

func (h *genHandler) onStart(ctx context.Context, info *callbacks.RunInfo,
	input callbacks.CallbackInput) context.Context {
	if !isModelCall(info) {
		return ctx
	}
	in := model.ConvCallbackInput(input)
	modelName := h.modelNameOf(ctx, info, in)

	name := "chat"
	if modelName != "" {
		name = "chat " + modelName
	}
	attrs := []attribute.KeyValue{
		attribute.String(attrLFType, "generation"),
		attribute.String(attrGenAIOp, "chat"),
		attribute.String(attrGenAISys, h.opt.system()),
	}
	if modelName != "" {
		attrs = append(attrs,
			attribute.String(attrLFModel, modelName),
			attribute.String(attrGenAIModel, modelName),
		)
	}
	attrs = append(attrs, h.labelAttrs(ctx)...)

	ctx, sp := genTracer.Start(ctx, name, trace.WithAttributes(attrs...))
	if in != nil && len(in.Messages) > 0 {
		if h.opt.canCapture() {
			if payload, ok := h.marshalInput(in.Messages); ok {
				sp.SetAttributes(attribute.String(attrLFInput, payload))
			}
		}
		sp.SetAttributes(attribute.Int(attrLFInputLen, totalRunes(in.Messages)))
	}
	return context.WithValue(ctx, genSpanKey{}, &genSpanState{span: sp})
}

// labelAttrs 归因标签 → span 属性（session_id 额外映射 Langfuse 会话键）。
func (h *genHandler) labelAttrs(ctx context.Context) []attribute.KeyValue {
	if h.opt.Labels == nil {
		return nil
	}
	labels := h.opt.Labels(ctx)
	if len(labels) == 0 {
		return nil
	}
	pfx := h.opt.prefix()
	attrs := make([]attribute.KeyValue, 0, len(labels)+1)
	for k, v := range labels {
		if k == "" || v == "" {
			continue
		}
		attrs = append(attrs, attribute.String(pfx+"."+k, v))
		if k == labelKeySession {
			attrs = append(attrs, attribute.String(attrLFSession, v))
		}
	}
	return attrs
}

func (h *genHandler) onEnd(ctx context.Context, _ *callbacks.RunInfo,
	output callbacks.CallbackOutput) context.Context {
	st := genStateFrom(ctx)
	if st == nil {
		return ctx
	}
	h.finish(st.span, model.ConvCallbackOutput(output), nil)
	return ctx
}

func (h *genHandler) onError(ctx context.Context, _ *callbacks.RunInfo, err error) context.Context {
	st := genStateFrom(ctx)
	if st == nil {
		return ctx
	}
	h.finish(st.span, nil, err)
	return ctx
}

// onStreamEnd 流式收口：只登记 goroutine，立刻返回（同步排空会阻塞真流式）。
func (h *genHandler) onStreamEnd(ctx context.Context, _ *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	st := genStateFrom(ctx)
	if st == nil {
		output.Close()
		return ctx
	}
	// GoSafe：panic 收进 async.PanicHandler（仓规：禁止裸 go func）。
	async.GoSafe(func() {
		defer output.Close()
		h.drainAndFinish(st.span, output)
	})
	return ctx
}

// drainAndFinish 排空副本流：累计 delta 正文（eino 流式每帧只带增量）与末帧用量。
func (h *genHandler) drainAndFinish(sp trace.Span, sr *schema.StreamReader[callbacks.CallbackOutput]) {
	// 字节预算 = rune 上限 ×4（UTF-8 单 rune 最多 4 字节）；收口时再按 rune 截断。
	usage, _, finish, content, reasoning := DrainStreamContent(sr, h.opt.maxChars()*4)
	h.setUsage(sp, usage, finish, content, reasoning)
	sp.End()
}

// finish 非流式收口（out=nil 且 err!=nil 走错误路径）。
func (h *genHandler) finish(sp trace.Span, out *model.CallbackOutput, callErr error) {
	if callErr != nil {
		h.markError(sp, callErr)
		sp.End()
		return
	}
	if out == nil {
		sp.End()
		return
	}
	content, reasoning := "", ""
	if out.Message != nil {
		content, reasoning = out.Message.Content, out.Message.ReasoningContent
	}
	h.setUsage(sp, TokenUsageOf(out), finishReasonOf(msgOf(out)), content, reasoning)
	sp.End()
}

func msgOf(out *model.CallbackOutput) *schema.Message {
	if out == nil {
		return nil
	}
	return out.Message
}

// setUsage 落输出正文/用量/finish（正文受采集开关控制）。
func (h *genHandler) setUsage(sp trace.Span, usage *model.TokenUsage, finish,
	content, reasoning string) {
	if h.opt.canCapture() {
		if payload, ok := h.marshalOutput(content, reasoning); ok {
			sp.SetAttributes(attribute.String(attrLFOutput, payload))
		}
	}
	// 长度恒落（0 也是事实）：缺席会让「模型没输出」与「这版没写该属性」不可区分。
	sp.SetAttributes(attribute.Int(attrLFOutputLen,
		utf8.RuneCountInString(content)+utf8.RuneCountInString(reasoning)))
	if finish != "" {
		sp.SetAttributes(attribute.StringSlice(attrGenAIFinish, []string{finish}))
	}
	if usage == nil {
		return
	}
	sp.SetAttributes(
		attribute.Int(attrGenAIInTok, usage.PromptTokens),
		attribute.Int(attrGenAIOutTok, usage.CompletionTokens),
	)
	details := map[string]int{
		"input":  usage.PromptTokens,
		"output": usage.CompletionTokens,
		"total":  usage.TotalTokens,
	}
	if usage.PromptTokenDetails.CachedTokens > 0 {
		details["cached"] = usage.PromptTokenDetails.CachedTokens
	}
	if usage.CompletionTokensDetails.ReasoningTokens > 0 {
		details["reasoning"] = usage.CompletionTokensDetails.ReasoningTokens
	}
	if b, err := json.Marshal(details); err == nil {
		sp.SetAttributes(attribute.String(attrLFUsage, string(b)))
	}
}

// markError 错误收口：Langfuse level/status_message + OTel 标准错误面。
func (h *genHandler) markError(sp trace.Span, err error) {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "unknown error"
	}
	msg = truncateRunes(h.opt.clean(msg), h.opt.maxChars())
	sp.SetAttributes(
		attribute.String(attrLFLevel, "ERROR"),
		attribute.String(attrLFStatus, msg),
	)
	sp.RecordError(err)
	sp.SetStatus(codes.Error, msg)
}

// modelNameOf 模型名解析：eino 回调携带的真实模型名 > 组件标识。
func (h *genHandler) modelNameOf(ctx context.Context, info *callbacks.RunInfo, in *model.CallbackInput) string {
	if in != nil && in.Config != nil && in.Config.Model != "" {
		return in.Config.Model
	}
	if h.opt.ModelName != nil {
		if name := h.opt.ModelName(ctx); name != "" {
			return name
		}
	}
	return modelOf(info)
}

// marshalInput 输入消息 → Langfuse 可读 JSON。
//
// 逐条限宽（budget/n）而不是整串截断：整串截断会打断 JSON，Langfuse UI 只能当
// 纯文本显示；逐条限宽保住结构，总量仍有界。
func (h *genHandler) marshalInput(msgs []*schema.Message) (string, bool) {
	if len(msgs) == 0 {
		return "", false
	}
	limit := h.opt.maxChars()
	per := limit / len(msgs)
	if per < 160 {
		per = 160 // 单条下限：过短会让「说了什么」不可读
	}
	type toolCall struct {
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
		Args string `json:"arguments,omitempty"`
	}
	type message struct {
		Role       string     `json:"role"`
		Content    string     `json:"content,omitempty"`
		Reasoning  string     `json:"reasoning,omitempty"`
		Name       string     `json:"name,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
		ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	}
	out := make([]message, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		rec := message{
			Role:       string(m.Role),
			Content:    truncateRunes(h.opt.clean(m.Content), per),
			Reasoning:  truncateRunes(h.opt.clean(m.ReasoningContent), per),
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			rec.ToolCalls = append(rec.ToolCalls, toolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: truncateRunes(h.opt.clean(tc.Function.Arguments), per),
			})
		}
		out = append(out, rec)
	}
	if len(out) == 0 {
		return "", false
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// marshalOutput 输出正文 → Langfuse 可读 JSON（content/reasoning 两字段）。
func (h *genHandler) marshalOutput(content, reasoning string) (string, bool) {
	if content == "" && reasoning == "" {
		return "", false
	}
	limit := h.opt.maxChars()
	payload := map[string]string{
		"role":    "assistant",
		"content": truncateRunes(h.opt.clean(content), limit),
	}
	if reasoning != "" {
		payload["reasoning"] = truncateRunes(h.opt.clean(reasoning), limit)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func genStateFrom(ctx context.Context) *genSpanState {
	st, _ := ctx.Value(genSpanKey{}).(*genSpanState)
	return st
}

// totalRunes 输入侧正文长度（rune 计数：Content + ReasoningContent）。
func totalRunes(msgs []*schema.Message) int {
	n := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		n += utf8.RuneCountInString(m.Content) + utf8.RuneCountInString(m.ReasoningContent)
	}
	return n
}

// truncateRunes 按 rune 截断并留痕（截断事实必须可见，不静默丢尾）。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	n := utf8.RuneCountInString(s)
	if n <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…[truncated " + strconv.Itoa(n-max) + " chars]"
}
