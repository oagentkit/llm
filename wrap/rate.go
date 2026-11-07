// 限速装饰器：BaseChatModel 直调旁路的速率治理。直调旁路（ReAct/RawModel）
// 绕过 Generator 链（llm.Client/Resilient）的限速是历史缺口；RateModel 挂进
// 包装链后，直调出口每次物理调用先经全局 token bucket 等待——与 Generator
// 链同桶，真全局。限速器类型为 llm.Waiter 最小面（与 Client.Limiter 同型
// 可共享同一实例；标准构造 ratelimit.NewTokenBucketFloat）。
package wrap

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm"
)

// RateModel 限速装饰器；Limiter nil = 直通。
type RateModel struct {
	einomodel.BaseChatModel
	Limiter llm.Waiter
}

var _ einomodel.ToolCallingChatModel = (*RateModel)(nil)

// IsCallbacksEnabled 实现 eino components.Checker（oagentkit/llm#86）：
// 转发内层模型的判定。装饰器自身不发回调——回调全部由内层模型自燃；
// 不透传时 compose 会在节点外再注入一轮 OnStart/OnEnd，一次调用产出
// 两条 usage 记录（预算/成本双计）。
func (m *RateModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.BaseChatModel)
}

var _ components.Checker = (*RateModel)(nil)

// NewRateModel 包装 inner；limiter 为 nil 时返回原对象（零开销直通）。
func NewRateModel(inner einomodel.BaseChatModel, limiter llm.Waiter) einomodel.BaseChatModel {
	if limiter == nil {
		return inner
	}
	return &RateModel{BaseChatModel: inner, Limiter: limiter}
}

func (m *RateModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	if err := m.wait(ctx); err != nil {
		return nil, err
	}
	return m.BaseChatModel.Generate(ctx, input, opts...)
}

func (m *RateModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	if err := m.wait(ctx); err != nil {
		return nil, err
	}
	return m.BaseChatModel.Stream(ctx, input, opts...)
}

func (m *RateModel) wait(ctx context.Context) error {
	if m.Limiter == nil {
		return nil
	}
	return m.Limiter.Wait(ctx)
}

// WithTools 工具绑定透传：绑定产物保持限速（与 Fit/Continue 同纪律）。
func (m *RateModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, ok := m.BaseChatModel.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, errors.New("ctxrate: 内层模型不支持工具绑定")
	}
	nb, err := bound.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &RateModel{BaseChatModel: nb, Limiter: m.Limiter}, nil
}
