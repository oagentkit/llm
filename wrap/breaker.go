// 熔断装饰器：BaseChatModel 直调旁路的按模型故障隔离。直调旁路（ReAct/
// RawModel）绕过 Generator 链后没有熔断——上游持续 5xx/超时时每次调用都真打
// 过去，白烧预算还拖死调用方。BreakerModel 挂进包装链后，每次物理调用先经按
// 名熔断器放行：熔断打开直接 ErrCircuitOpen 快速失败（不烧限速令牌、不占在飞
// 名额——所以推荐组装顺序里 Breaker 在 Governor/Rate 之外），放行则按真实结果
// 回填成败：连续 trip 次失败切 open，冷却期满放行半开探测，探测成功关闸、失败
// 重开并重置冷却。
//
// 熔断内核单源 oekit/concurrency/breaker（GenBreaker：放行与回报的代绑定，
// 陈旧代整体作废——探测失联误判不污染统计）。回填纪律沿用 port.Breakers
// （llm.Resilient 同款，Gen 系回报而非 Promise——内核 isProbeGen 对探测身份的
// 按代推断恒可靠）：成功 → SuccessGen；ctx 取消（用户主动中止，结果未知）→
// AbandonGen——不计失败，连续用户取消不把模型误熔断；其余失败 → FailureGen
// （内核对已取消 ctx 自带统计门，双保险）。流式调用：干净 EOF = 成功；流中错误
// 按上款分类回填；消费方提前收流 = 结果未知，AbandonGen。
//
// WithTools 派生共享状态（issue #198 核心契约）：与 GovernorModel 同款——派生
// 产物必须共享同一熔断状态，否则 ReAct 每轮派生一枚新熔断器，「连挂 N 次跳闸」
// 永远凑不齐。本装饰器派生时原样传递 st 指针，测试钉死。
package wrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/port"
	"github.com/oekit/concurrency/async"
	"github.com/oekit/concurrency/breaker"
)

// BreakerModel 熔断装饰器。st 为 unexported：外部只能经 NewBreakerModel 构造
// （零值误用会在 AllowGen 处显式 panic，而非静默不熔断）。
type BreakerModel struct {
	einomodel.BaseChatModel
	// Name 熔断器标识（通常 = 模型名）：拒绝错误与拒绝回调按名留痕。
	Name string
	// st 按名熔断状态（内核单源 oekit/concurrency/breaker）；WithTools 派生时
	// 指针共享——issue #198 契约「派生即新状态 = 熔断失效」的防线。
	st breaker.GenBreaker
}

var _ einomodel.ToolCallingChatModel = (*BreakerModel)(nil)

// IsCallbacksEnabled 实现 eino components.Checker（oagentkit/llm#86）：
// 转发内层模型的判定。装饰器自身不发回调——回调全部由内层模型自燃；
// 不透传时 compose 认为组件「不会自己发回调」，在节点外再注入一轮
// OnStart/OnEnd，一次调用产出两条 usage 记录（预算/成本双计）。
func (m *BreakerModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.BaseChatModel)
}

var _ components.Checker = (*BreakerModel)(nil)

// NewBreakerModel 包装 inner：连续 trip 次失败切 open（与内核速率型自适应触发
// 并联），冷却 cooldown 后放行半开探测。
//
// trip/cooldown ≤0 取家族缺省（port.DefaultBreakerTrip=3 /
// port.DefaultBreakerCooldown=5min）——ResilientConfig.fillDefaults 同口径：
// 配置驱动的装配漏配时保护默认开启，而非静默关闭或构造 panic。opts 后置追加，
// 可显式覆盖基线（如 WithConsecutiveFailures(0) 关闭计数型仅留速率型自适应、
// WithProbeTimeout 调大探测时限、WithBreakerClock 注入测试时钟、
// WithBreakerRejectHandler 接观测）。
//
// 每次构造独立新实例（内核 NewBreaker 语义）：同模型多个装配点各自独立熔断，
// 互不共享——进程级按名共享请收口到 port.Breakers 后再装配（issue #198 只要求
// WithTools 派生共享，跨装配点共享不预导出）。探测时限缺省 1min（内核
// DefaultProbeTimeout）：Breaker 位于治理层之外时，探测窗口含限速等待与在飞
// 排队，队列量级大需经 WithProbeTimeout 显式调大（代绑定保证失联误判不污染
// 统计，只影响半开单探测隔离的收口速度）。
func NewBreakerModel(inner einomodel.BaseChatModel, name string, trip int, cooldown time.Duration, opts ...breaker.BreakerOption) *BreakerModel {
	if trip <= 0 {
		trip = port.DefaultBreakerTrip
	}
	if cooldown <= 0 {
		cooldown = port.DefaultBreakerCooldown
	}
	all := make([]breaker.BreakerOption, 0, len(opts)+2)
	all = append(all,
		breaker.WithConsecutiveFailures(trip),
		breaker.WithCooldown(cooldown),
	)
	all = append(all, opts...)
	return &BreakerModel{
		BaseChatModel: inner,
		Name:          name,
		st:            newBreakerState(name, all),
	}
}

// newBreakerState 构造熔断状态（内核单源；*circuitBreaker 恒实现 GenBreaker，
// 断言失败仅产生 nil——与 port.NewBreakers 同款信任内核的口径）。
func newBreakerState(name string, opts []breaker.BreakerOption) breaker.GenBreaker {
	gb, _ := breaker.NewBreaker(name, opts...).(breaker.GenBreaker)
	return gb
}

func (m *BreakerModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	_, gen, err := m.st.AllowGen(ctx)
	if err != nil {
		if errors.Is(err, breaker.ErrCircuitOpen) {
			return nil, fmt.Errorf("wrap: breaker 模型 %s 熔断中: %w", m.Name, err)
		}
		return nil, err // ctx 已取消：调用方自己的 ctx 错误原样上抛
	}
	msg, err := m.BaseChatModel.Generate(ctx, input, opts...)
	m.report(ctx, gen, err)
	return msg, err
}

func (m *BreakerModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	_, gen, err := m.st.AllowGen(ctx)
	if err != nil {
		if errors.Is(err, breaker.ErrCircuitOpen) {
			return nil, fmt.Errorf("wrap: breaker 模型 %s 熔断中: %w", m.Name, err)
		}
		return nil, err
	}
	sr, err := m.BaseChatModel.Stream(ctx, input, opts...)
	if err != nil {
		m.report(ctx, gen, err)
		return nil, err
	}
	// 结果回填覆盖整段流消费（与在飞闸同口径：流未读完 = 调用未结束）。
	out, w := schema.Pipe[*schema.Message](1)
	async.GoSafe(func() {
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// 干净收口 = 成功（eino Close 纪律详见 FitModel.observeUsage）
					m.st.SuccessGen(gen)
					sr.Close()
					w.Close()
					return
				}
				_ = w.Send(nil, err)
				sr.Close() // eino 契约：Recv 出错不内部关流，须显式 Close
				m.report(ctx, gen, err)
				return
			}
			// Send 返回 true = 消费端已提前 Close：结果未知，按用户中止口径
			// Abandon（不计失败也不计成功——消费方主动收流不该推动熔断状态）
			if w.Send(chunk, nil) {
				sr.Close()
				m.st.AbandonGen(gen)
				return
			}
		}
	})
	return out, nil
}

// report 回填一次完整调用的结果（port.Breakers 同款纪律）：成功 SuccessGen；
// ctx 取消（用户主动中止，结果未知）AbandonGen——不计失败并解除半开探测占位；
// 其余失败 FailureGen（内核对已取消 ctx 自带统计门，双保险）。
func (m *BreakerModel) report(ctx context.Context, gen uint64, err error) {
	switch {
	case err == nil:
		m.st.SuccessGen(gen)
	case ctx.Err() != nil:
		m.st.AbandonGen(gen)
	default:
		m.st.FailureGen(ctx, gen)
	}
}

// WithTools 工具绑定转发：绑定产物再包一层 BreakerModel——**st 指针原样传递，
// 派生实例与原实例共享同一熔断状态**（issue #198 契约：eino ReAct 每轮重新
// 派生带工具模型，「派生即新状态」= 熔断失效，连挂计数永远凑不齐）。
func (m *BreakerModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, ok := m.BaseChatModel.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, fmt.Errorf("wrap: breaker 内层模型不支持工具绑定 name=%s type=%T", m.Name, m.BaseChatModel)
	}
	nb, err := bound.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &BreakerModel{BaseChatModel: nb, Name: m.Name, st: m.st}, nil
}
