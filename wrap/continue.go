// 输出截断续写（对标 ZCode turn-output-token-continuation）：finish reason 为
// length/max_tokens 且消息无工具调用时，保留部分输出并以「直接续写」指令再调，
// 内容顺序拼接，最多 maxContinuations 轮。被截断的评审结论不再整体作废重跑——
// 续写成本远低于整轮重跑，且拼接补全 JSON 恰好是 jsonrepair 的前置救济。
//
// 纪律：
//   - 带 ToolCalls 的截断消息不续写（ReAct 中间步的续写会污染工具调用状态）；
//   - 续写轮返回 ToolCalls 时以续写轮为基座合并——工具调用决策不得被静默
//     丢弃（llm#116：绑定工具的模型在续写轮决定发起工具调用是合法决策，
//     丢弃后 ReAct 循环把半截输出当最终答案）；
//   - 续写失败返回已得部分（部分结果优于整体失败）；
//   - Stream 直通不续写（当前无流式消费方，装饰器如实声明）。
package wrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// maxContinuations 续写轮数上限（ZCode 同款 3）。
const maxContinuations = 3

// continueDirective 续写指令（ZCode "Resume directly — no apology, no recap" 同义）。
const continueDirective = "直接从截断处继续输出剩余内容：不要重复已有内容，不要道歉或复述。"

// ContinueModel 输出截断续写装饰器；无可变状态，可跨任务共享。
type ContinueModel struct {
	einomodel.BaseChatModel
	Stage      string
	MaxRounds  int // ≤0 = maxContinuations
	OnContinue func(stage string, round, addedRunes int)
}

var _ einomodel.ToolCallingChatModel = (*ContinueModel)(nil)

// IsCallbacksEnabled 实现 eino components.Checker（oagentkit/llm#86）：
// 转发内层模型的判定。装饰器自身不发回调——回调全部由内层模型自燃；
// 不透传时 compose 会在节点外再注入一轮 OnStart/OnEnd，一次调用产出
// 两条 usage 记录（预算/成本双计）。
func (m *ContinueModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.BaseChatModel)
}

var _ components.Checker = (*ContinueModel)(nil)

// NewContinueModel 包装 inner；轮数上限 ≤0 时取缺省 3。
func NewContinueModel(inner einomodel.BaseChatModel, stage string, onContinue func(string, int, int)) *ContinueModel {
	return &ContinueModel{BaseChatModel: inner, Stage: stage, OnContinue: onContinue}
}

func (m *ContinueModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	msg, err := m.BaseChatModel.Generate(ctx, input, opts...)
	if err != nil || !isTruncated(msg) {
		return msg, err
	}
	max := m.MaxRounds
	if max <= 0 {
		max = maxContinuations
	}
	for round := 1; round <= max && isTruncated(msg); round++ {
		cont := make([]*schema.Message, 0, len(input)+2)
		cont = append(cont, input...)
		cont = append(cont,
			&schema.Message{Role: schema.Assistant, Content: msg.Content},
			&schema.Message{Role: schema.User, Content: continueDirective})
		next, err := m.BaseChatModel.Generate(ctx, cont, opts...)
		if err != nil || next == nil {
			// 调用方取消/超时预算耗尽不是「续写失败」，必须原样上抛——吞掉会让
			// 上层把腰斩的半截输出当正常完成品（第八轮审计）；其余错误走部分返回
			if ctx.Err() != nil {
				return msg, ctx.Err()
			}
			// provider 侧 deadline（模型自身限时，外层 ctx 健康）同样不许把
			// 半截当完整品：包装后上抛（llm#69——此前 errors.Is 判到
			// DeadlineExceeded 时返回 ctx.Err()==nil，(半截, nil) 零错误成功）
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return msg, fmt.Errorf("ctxrate: 续写被 provider 侧取消/限时打断（半截输出，不当完整品）: %w", err)
			}
			return msg, nil // 续写失败：返回已得部分
		}
		// 续写轮发起工具调用（llm#116）：以续写轮为基座合并——ToolCalls 与
		// finish reason 保留（OpenAI 兼容端点上 tool_calls 与文本互斥，
		// next.Content 常为空、动作全在 ToolCalls），正文保住首轮已得的截断
		// 前缀。此前合并块恒以首轮为基座（ToolCalls 必为空），续写轮的
		// ToolCalls 被静默丢弃：ReAct 循环收到纯文本 assistant 消息，工具
		// 调用意图无声蒸发，agent 把半截输出当最终答案或重规划——静默走错。
		// 带 ToolCalls 的合并结果 isTruncated 恒 false，续写循环随之收口
		//（「带 ToolCalls 不续写」纪律对续写轮同样生效）。
		if len(next.ToolCalls) > 0 {
			merged := *next
			merged.Content = msg.Content + next.Content
			merged.ResponseMeta = mergeContinueMeta(msg.ResponseMeta, next.ResponseMeta)
			added := len([]rune(next.Content))
			msg = &merged
			if m.OnContinue != nil {
				m.OnContinue(m.Stage, round, added)
			}
			break
		}
		merged := *msg
		merged.Content = msg.Content + next.Content
		merged.ResponseMeta = mergeContinueMeta(msg.ResponseMeta, next.ResponseMeta)
		added := len([]rune(next.Content))
		msg = &merged
		if m.OnContinue != nil {
			m.OnContinue(m.Stage, round, added)
		}
	}
	return msg, nil
}

// mergeContinueMeta 续写轮 meta 合并（llm#82）：FinishReason 以末轮为准
// （下轮截断判定取最新语义）；Usage 逐轮累加——prompt 取首轮（整段上下文按
// 一份计，续写轮重复回传的部分不重复记账），completion 求和（真实总输出），
// total 重算为 prompt+completion 保持自洽（llm#195 起该口径与根包
// recordFromUsage 的「total 缺席兜底重算」互指同源）。此前以末轮 meta 整体
// 覆盖，首轮（通常占输出大头）的 usage 从最终消息上消失——下游按
// ResponseMeta.Usage 记账的消费方对「截断+续写」只计末轮，恰在长输出
// （成本最高）场景失真。
func mergeContinueMeta(first, last *schema.ResponseMeta) *schema.ResponseMeta {
	if last == nil {
		// 末轮无 meta：保持置 nil 的旧语义——isTruncated 随之落空、续写循环
		// 收口（不得沿用首轮的 length，否则对不回传 meta 的模型反复续写）
		return nil
	}
	merged := *last
	if first == nil || first.Usage == nil {
		return &merged // 首轮无 usage：末轮 usage 即总量，原样保留
	}
	if last.Usage == nil {
		// 末轮缺 usage（部分兼容端点不回传）：保住首轮 usage 不丢账
		u := *first.Usage
		merged.Usage = &u
		return &merged
	}
	u := *first.Usage
	u.CompletionTokens += last.Usage.CompletionTokens
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	merged.Usage = &u
	return &merged
}

// isTruncated 截断判定：finish reason 为 length/max_tokens 且无工具调用。
func isTruncated(msg *schema.Message) bool {
	if msg == nil || len(msg.ToolCalls) > 0 {
		return false
	}
	if msg.ResponseMeta == nil {
		return false
	}
	switch strings.ToLower(msg.ResponseMeta.FinishReason) {
	case "length", "max_tokens":
		return true
	}
	return false
}

// WithTools 工具绑定透传：绑定产物再包一层 ContinueModel（与 FitModel 同纪律）。
func (m *ContinueModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, ok := m.BaseChatModel.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, errors.New("ctxcontinue: 内层模型不支持工具绑定 stage=" + m.Stage)
	}
	nb, err := bound.WithTools(tools)
	if err != nil {
		return nil, err
	}
	c := *m
	c.BaseChatModel = nb
	return &c, nil
}
