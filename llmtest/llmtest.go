// Package llmtest eino ChatModel/Provider 的脚本化测试桩——llm/reflection/router
// 等包的测试共用（此前四份手写桩已漂移出两种耗尽语义与多种记录口径）。
// 只进测试二进制，不进任何生产依赖图。
package llmtest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Resp 单次调用的脚本条目（Generate 与 Stream 共按序消费，两路都按本条目
// 填 ResponseMeta——单源 respMetaOf，防两路漂移）。
type Resp struct {
	Content      string // 响应文本（Err 非空时忽略）
	Err          error  // 非空时本次调用返回该错误
	FinishReason string // 非空时填入 ResponseMeta（截断重试等场景断言用）
	// Prompt/Completion 任一非零时填入 ResponseMeta.Usage（真实 usage 记账断言用）。
	Prompt     int
	Completion int
	// ToolCalls 附带的工具调用（tool-calling 协议桩：adk planexecute 的
	// plan/respond 工具调用形态）。
	ToolCalls []schema.ToolCall
}

// ToolCall 便捷构造单工具调用条目（name/arguments JSON）。
func ToolCall(name, arguments string) []schema.ToolCall {
	return []schema.ToolCall{{
		ID:       "call_" + name,
		Type:     "function",
		Function: schema.FunctionCall{Name: name, Arguments: arguments},
	}}
}

// Model 脚本化 BaseChatModel 测试桩（并发安全）。
//
//   - Script 按调用序消费；RepeatLast=true 时耗尽后重复末条（重试/降级链测试：
//     持续错误才能触发切换），false 时耗尽报错（防桩被多调——脚本写漏立即可见）；
//   - FirstInput/LastInput 记录每次 Generate 的首/末消息文本（提示词断言用）；
//   - StreamContent 非空时 Stream 返回该内容的单块流（优先于脚本，**不取脚本
//     条目**——llm#196：脚本序只由真正取脚本的调用推进，此前以 Calls 为脚本
//     下标，捷径推进 Calls 会把脚本序号凭空推进一格，随后的 Generate 取到
//     错位条目）；空则回落到脚本条目——Generate/Stream 共享同一脚本序；
//   - Calls 为 Generate+Stream 总调用数（含脚本耗尽/空脚本那几次报错的调用——
//     与耗尽错误里的「第 N 次调用」同源，报的是真实调用序）。
type Model struct {
	mu sync.Mutex

	Script     []Resp
	RepeatLast bool
	// StreamContent Stream 路径的单块内容：非空时优先于脚本（不消费脚本条目），
	// 空 = 按脚本条目出流。
	StreamContent string

	// 断言面（测试读取）。
	FirstInput string   // 最近一次 Generate 的首条消息文本
	LastInput  string   // 最近一次 Generate 的末条消息文本
	Inputs     []string // 每次 Generate 的末条消息文本（调用序历史）
	// Heads 每次调用的首条消息文本（首条通常是 system 提示词——按调用序断言用）。
	Heads []string
	// Flats 每次调用的全部消息按 "role:content" 扁平拼接（多 system 消息共存的
	// 提示词组装断言用）。
	Flats []string
	Calls int

	// scriptIdx 已消费的脚本条目数（llm#196）：与 Calls 解耦——StreamContent
	// 恒定内容流不取脚本条目，脚本序只由真正取脚本的调用推进；混合形态
	// （Script + StreamContent 同置）下「脚本写漏立即可见」的耗尽保护不失效。
	scriptIdx int
}

// Generate 按 Script 依次响应。
func (m *Model) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(in) > 0 {
		m.FirstInput = in[0].Content
		m.LastInput = in[len(in)-1].Content
		m.Inputs = append(m.Inputs, m.LastInput)
		m.Heads = append(m.Heads, in[0].Content)
		m.Flats = append(m.Flats, flatMsgs(in))
	}
	resp, err := m.next()
	if err != nil {
		return nil, err
	}
	if resp.Err != nil {
		return nil, resp.Err
	}
	out := &schema.Message{Role: schema.Assistant, Content: resp.Content, ToolCalls: resp.ToolCalls,
		ResponseMeta: respMetaOf(resp)}
	return out, nil
}

// respMetaOf 按脚本条目构造 ResponseMeta（Generate 与 Stream 的单源——
// oagentkit/llm#166：此前 Stream 完全不填，同一 Resp 条目走 Stream 的
// usage/finish_reason 恒为 nil，mock 与真实 provider 契约分叉）。
func respMetaOf(resp Resp) *schema.ResponseMeta {
	// Prompt/Completion 任一非零即落 Usage（oagentkit/llm#167A：此前判据只看
	// Prompt，只给 Completion 的条目整份 ResponseMeta 都不生成、连 FinishReason
	// 也一并丢——与「Prompt/Completion 非零时填入」的字段文档相悖）。
	if resp.FinishReason == "" && resp.Prompt == 0 && resp.Completion == 0 {
		return nil
	}
	meta := &schema.ResponseMeta{FinishReason: resp.FinishReason}
	if resp.Prompt > 0 || resp.Completion > 0 {
		meta.Usage = &schema.TokenUsage{
			PromptTokens: resp.Prompt, CompletionTokens: resp.Completion,
			TotalTokens: resp.Prompt + resp.Completion,
		}
	}
	return meta
}

// Stream 按同一脚本响应（Generate/Stream 共享调用序）：条目带 ToolCalls 时
// 发带工具调用的消息，否则发 Content；ResponseMeta 与 Generate 同源填充。
// StreamContent 非空时优先恒定内容（简洁的单内容流场景），空则回落到脚本条目。
func (m *Model) Stream(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 与 Generate 同口径记录输入（提示词断言用——adk 部分路径走 Stream）
	if len(in) > 0 {
		m.FirstInput = in[0].Content
		m.LastInput = in[len(in)-1].Content
		m.Inputs = append(m.Inputs, m.LastInput)
		m.Heads = append(m.Heads, in[0].Content)
		m.Flats = append(m.Flats, flatMsgs(in))
	}
	if m.StreamContent != "" {
		// 恒定内容流不取脚本条目（llm#196）：只递增 Calls（真实调用序），脚本
		// 序号由 next() 的 scriptIdx 独立推进——此前复用 Calls 作脚本下标，
		// 捷径把序号凭空推进一格，随后 Generate 取到错位条目，耗尽保护失效。
		m.Calls++
		return msgStream(&schema.Message{Role: schema.Assistant, Content: m.StreamContent}), nil
	}
	resp, err := m.next()
	if err != nil {
		return nil, err
	}
	if resp.Err != nil {
		return nil, resp.Err
	}
	return msgStream(&schema.Message{
		Role:         schema.Assistant,
		Content:      resp.Content,
		ToolCalls:    resp.ToolCalls,
		ResponseMeta: respMetaOf(resp),
	}), nil
}

// flatMsgs 消息序列扁平串（提示词组装断言用）。
func flatMsgs(in []*schema.Message) string {
	parts := make([]string, 0, len(in))
	for _, m := range in {
		parts = append(parts, string(m.Role)+":"+m.Content)
	}
	return strings.Join(parts, "\n")
}

// msgStream 单块消息流（llmtest 的流恒为单块：Pipe(1) 预填一帧后 Close——
// ToolModel 的 ToolTag 改写依赖这条不变量同步取帧，不需要转发 goroutine）。
func msgStream(msg *schema.Message) *schema.StreamReader[*schema.Message] {
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr
}

// next 取下一条脚本（内部已持锁）。每次真实调用（含空脚本/耗尽报错的那几次）
// 都在这里自增 Calls——让 Calls 与错误消息里的「第 N 次调用」同源反映真实
// 调用序（oagentkit/llm#167D：此前耗尽分支不自增、Calls 停在 len(Script)，
// 连续耗尽永远报同一个「第 2 次调用」）。
// 脚本位置用独立的 scriptIdx（llm#196）：只在真正取走一个条目时推进——
// StreamContent 捷径只递增不取条目，脚本序不再被凭空跳格。
func (m *Model) next() (Resp, error) {
	m.Calls++
	if len(m.Script) == 0 {
		return Resp{}, fmt.Errorf("llmtest: 脚本为空（第 %d 次调用）", m.Calls)
	}
	if m.scriptIdx >= len(m.Script) {
		if m.RepeatLast {
			return m.Script[len(m.Script)-1], nil
		}
		return Resp{}, fmt.Errorf("llmtest: 脚本耗尽（第 %d 次调用）", m.Calls)
	}
	resp := m.Script[m.scriptIdx]
	m.scriptIdx++
	return resp, nil
}

// ToolModel 带 WithTools 派生能力的桩（ADK 绑工具路径的降级语义测试用；
// Model 本身不实现 ToolCallingChatModel——不支持绑定的场景直接用 Model）。
type ToolModel struct {
	*Model
	// ToolTag WithTools 派生副本的 Content 后缀（区分派生前后的响应）：
	// 只在 WithTools 返回的派生副本上追加，父桩自身不加。
	// oagentkit/llm#167B：此前该字段只被原样复制、从未拼进 Content，
	// 设了也完全不起作用（死字段）。
	ToolTag string
	// derived 标记 WithTools 派生副本——ToolTag 只在派生副本上生效。
	derived bool
}

var _ model.ToolCallingChatModel = (*ToolModel)(nil)

// WithTools 返回带 ToolTag 标记的派生副本（脚本与状态共享父桩）。
func (t *ToolModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &ToolModel{Model: t.Model, ToolTag: t.ToolTag, derived: true}, nil
}

// Generate 按共享脚本响应；派生副本在 Content 末尾追加 ToolTag。
func (t *ToolModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	out, err := t.Model.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	t.tagContent(out)
	return out, nil
}

// Stream 同 Generate 的 ToolTag 口径。单块流同步取帧改写即可
// （msgStream 不变量，见其注释）。
func (t *ToolModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := t.Model.Stream(ctx, in, opts...)
	if err != nil || !t.derived || t.ToolTag == "" {
		return sr, err
	}
	msg, rerr := sr.Recv()
	sr.Close()
	if rerr != nil {
		return nil, rerr
	}
	t.tagContent(msg)
	return msgStream(msg), nil
}

// tagContent 派生副本给响应 Content 追加 ToolTag（父桩 derived=false 原样透传）。
func (t *ToolModel) tagContent(msg *schema.Message) {
	if t.derived && t.ToolTag != "" && msg != nil {
		msg.Content += t.ToolTag
	}
}

// Provider llm.Provider 接口的测试实现（Resilient 降级链测试用）。
type Provider struct {
	// Label Name() 返回值（字段不能与接口方法 Name() 同名）。
	Label      string
	ModelValue model.BaseChatModel
	CtxTokens  int
	MaxOut     int
	// Costs CostPer1KTokens 返回值 [prompt, completion]。
	Costs [2]float64
	// Timeout AttemptTimeout 返回值（>0 时生效；Resilient 单次尝试限时测试用）。
	Timeout time.Duration
}

var _ interface {
	Name() string
	Model() model.BaseChatModel
	ModelName() string
	ContextTokens() int
	MaxOutputTokens() int
	CostPer1KTokens() (float64, float64)
} = (*Provider)(nil)

func (p *Provider) Name() string                        { return p.Label }
func (p *Provider) Model() model.BaseChatModel          { return p.ModelValue }
func (p *Provider) ModelName() string                   { return p.Label + "-model" }
func (p *Provider) ContextTokens() int                  { return p.CtxTokens }
func (p *Provider) MaxOutputTokens() int                { return p.MaxOut }
func (p *Provider) CostPer1KTokens() (float64, float64) { return p.Costs[0], p.Costs[1] }

// AttemptTimeout 可选能力（llm.timeoutProvider 接口；0=不限）。
func (p *Provider) AttemptTimeout() time.Duration { return p.Timeout }

// NewScriptedProvider 便捷构造：name + 脚本（耗尽重复末条——降级链测试依赖
// 持续错误触发切换），窗口 128k / 输出 4096 / 成本 0.001|0.002。
func NewScriptedProvider(name string, script ...Resp) *Provider {
	return &Provider{
		Label:      name,
		ModelValue: &Model{Script: script, RepeatLast: true},
		CtxTokens:  128_000,
		MaxOut:     4096,
		Costs:      [2]float64{0.001, 0.002},
	}
}
