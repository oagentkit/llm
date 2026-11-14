// 在飞闸装饰器：BaseChatModel 直调旁路的按模型并发封顶。直调旁路（ReAct/
// RawModel）绕过 Generator 链后没有 per-模型并发上限——上游抖动时一个模型的
// 在飞调用可以无界堆积（连接池/配额/下游过载全暴露）。GovernorModel 挂进包装
// 链后，每次物理调用先按闸门 key 取一个在飞名额：名额耗尽则排队等待（ctx 取消
// 即中止），调用结束归还。流式调用的名额覆盖整段消费——流未读完 = 调用未结束。
//
// 闸门内核单源 oekit/concurrency syncx.KeyedGate（key 分档位「每个模型最多
// N 个在飞」）；cap≤0 内核天然直通，Gate 为 nil 时装饰器整体直通（零开销）。
//
// WithTools 派生共享状态（issue #198 核心契约）：eino ReAct 每轮重新派生带
// 工具模型，派生产物必须共享同一闸门——「派生即新状态」= 闸门失效（每轮一枚
// 新闸，在飞封顶形同虚设）。本装饰器派生时原样传递 Gate 指针，测试钉死。
package wrap

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/concurrency/syncx"
)

// GovernorModel 在飞闸装饰器；Gate nil = 直通。
type GovernorModel struct {
	einomodel.BaseChatModel
	// Name 闸门 key（通常 = 模型名）：同 key 的调用共享同一在飞名额池，
	// 排队中止错误按名留痕。
	Name string
	// Gate 在飞闸（每 key 名额 = 构造时 cap；cap≤0 内核直通）。nil = 直通。
	Gate *syncx.KeyedGate
}

var _ einomodel.ToolCallingChatModel = (*GovernorModel)(nil)

// IsCallbacksEnabled 实现 eino components.Checker（oagentkit/llm#86）：
// 转发内层模型的判定。装饰器自身不发回调——回调全部由内层模型自燃；
// 不透传时 compose 认为组件「不会自己发回调」，在节点外再注入一轮
// OnStart/OnEnd，一次调用产出两条 usage 记录（预算/成本双计）。
func (m *GovernorModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.BaseChatModel)
}

var _ components.Checker = (*GovernorModel)(nil)

// NewGovernorModel 包装 inner；gate 为 nil 时保持直通（不改行为）。
func NewGovernorModel(inner einomodel.BaseChatModel, name string, gate *syncx.KeyedGate) *GovernorModel {
	return &GovernorModel{BaseChatModel: inner, Name: name, Gate: gate}
}

func (m *GovernorModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	release, err := m.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return m.BaseChatModel.Generate(ctx, input, opts...)
}

func (m *GovernorModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	release, err := m.acquire(ctx)
	if err != nil {
		return nil, err
	}
	sr, err := m.BaseChatModel.Stream(ctx, input, opts...)
	if err != nil {
		release()
		return nil, err
	}
	// 在飞名额覆盖整段流消费：转发收口（EOF/错误/消费方提前关闭）时归还。
	out, w := schema.Pipe[*schema.Message](1)
	async.GoSafe(func() {
		defer release()
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// eino 契约：reader 必须恰好 Close 一次，EOF 后也必须 Close
					//（Close 纪律详见 FitModel.observeUsage 注释）。
					sr.Close()
					w.Close()
					return
				}
				_ = w.Send(nil, err)
				sr.Close() // eino 契约：Recv 出错不内部关流，须显式 Close
				return
			}
			// Send 返回 true = 消费端已提前 Close，不再投递
			if w.Send(chunk, nil) {
				sr.Close()
				return
			}
		}
	})
	return out, nil
}

// acquire 取在飞名额：Gate nil = 直通（no-op release）。排队中被 ctx 取消返回
// 错误（名额未取得，无从归还）；包一层带 key 的文案——裸 ctx.Err() 丢失
// 「曾排队」信息，多模型并发时无从定位是哪个模型的闸。
func (m *GovernorModel) acquire(ctx context.Context) (func(), error) {
	if m.Gate == nil {
		return func() {}, nil
	}
	release, err := m.Gate.Acquire(ctx, m.Name)
	if err != nil {
		return nil, fmt.Errorf("wrap: governor 模型 %s 在飞排队被中止（名额未取得）: %w", m.Name, err)
	}
	return release, nil
}

// WithTools 工具绑定转发：绑定产物再包一层 GovernorModel——**Gate 指针原样
// 传递，派生实例与原实例共享同一在飞闸**（issue #198 契约：eino ReAct 每轮
// 重新派生带工具模型，「派生即新状态」= 闸门失效，在飞封顶形同虚设）。
func (m *GovernorModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, ok := m.BaseChatModel.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, fmt.Errorf("wrap: governor 内层模型不支持工具绑定 name=%s type=%T", m.Name, m.BaseChatModel)
	}
	nb, err := bound.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &GovernorModel{BaseChatModel: nb, Name: m.Name, Gate: m.Gate}, nil
}
