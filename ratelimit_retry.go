// 模型级限流退避装饰器（llm#100）：RawModel 直驱路径的 429 退避。
//
// llm 的弹性栈（Resilient 降级链 + 熔断 + 预算，Client.generateRetry 同模型
// 重试）只覆盖 Generator 自家的 Generate/GenerateJSON 路径；RawModel() 返回的
// 裸 eino 模型被 ReAct agent 等直接驱动（工具循环里逐轮调
// GenerateWithOptions/Stream）时，429 退避重试无人管——一次失败即硬错
// （消费方实证：bianque 为此手搓了完整装饰器，552 次 429 灭三专家）。
//
// 本装饰器把「单模型 429 退避」补到模型面，与 breaker/限速装饰器同族、可挂
// 包装链。分层关系：**装饰器管单模型退避，Resilient/FailoverModel 管跨模型
// 切换**——429 在本层耗尽 attempts 后上抛，交上层降级链决策。退避曲线与
// Client.generateRetry 同源（waitBackoffGap/backoffDelayJittered 单源实现），
// Retry-After 单源复用 oekit/pkg/httpjson（传输层捕获 → sink → 取舍钳制）。
//
// 装饰位指引：本装饰器应包在限速治理层**之外**——
//
//	llm.WithRateLimitRetry(llmwrap.NewRateModel(inner, limiter))  // ✓
//
// 每次物理调用（含重试）都先过限速桶；退避等待本身不持任何在飞令牌，重试
// 重入限速层时重新排队，与 bianque 实证形态一致（包装位=并发治理之外，退避
// 等待不占在飞配额）。
//
// 刻意的边界：只重试限流类失败（ClassifyLLMError→IsRateLimit）——本装饰器的
// 语义就是「429 退避」，非限流错误（401/上下文超限/截断/确定性 4xx）原样
// 上抛，交上层（Generator 重试骨架或降级链）按各自策略处置；流一旦建立即
// 原样交还调用方（建流后的流生命周期归消费方，本层不触碰——agent#77 的
// exactly-once Close 纪律在消费侧）。ctx 取消立即让位，不烧退避窗口。
//
// 观测与判定注入（llm#209）：WithOnRetry/WithOnExhausted 把「确实重试」与
// 「重试耗尽=失败定型」落进宿主事件与指标；耗尽错误类型化为
// *RetryExhaustedError（errors.As 可判 + Retryable 自报瞬态）；判定面可经
// WithRetryPredicate 按宿主错误码体系覆盖（缺省与今日逐字一致）。
package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/oekit/pkg/mathx"
)

// 缺省曲线 = bianque 生产实证形态（配额型供应商突发限流的踩坑收敛值：
// 指数 2s→32s 共 5 次 + 0.25 抖动——并行扇出场景多专家同拍撞限流，无抖动的
// 等比退避会整队同步重撞）。
const (
	defaultRetryAttempts    = 5 // 总尝试次数（含首次）
	defaultRetryBaseDelay   = 2 * time.Second
	defaultRetryMaxDelay    = 32 * time.Second
	defaultRetryJitterRatio = 0.25
)

// RateLimitRetryOption WithRateLimitRetry 的选项（命名遵矩阵 XxxOption 纪律）。
type RateLimitRetryOption func(*rateLimitRetryConfig)

// WithRetryAttempts 总尝试次数上限（含首次），<=0 取缺省 5。
func WithRetryAttempts(n int) RateLimitRetryOption {
	return func(c *rateLimitRetryConfig) { c.attempts = n }
}

// WithRetryBaseDelay 退避基准（首拍等待），<=0 取缺省 2s。
func WithRetryBaseDelay(d time.Duration) RateLimitRetryOption {
	return func(c *rateLimitRetryConfig) { c.base = d }
}

// WithRetryMaxDelay 退避封顶，<=0 取缺省 32s。
func WithRetryMaxDelay(d time.Duration) RateLimitRetryOption {
	return func(c *rateLimitRetryConfig) { c.ceil = d }
}

// WithRetryJitterRatio 单边向上抖动比例 [0,1]（退避 ∈ [d, d(1+r))）：
// 0 = 关抖动（确定性，测试依赖）；缺省 0.25。越界取缺省。
func WithRetryJitterRatio(r float64) RateLimitRetryOption {
	return func(c *rateLimitRetryConfig) { c.jitterRatio = r }
}

// WithOnRetry 重试观测钩子（llm#209）：每次「确实重试」时回调——时点在
// 退避等待**结束后、下次物理调用发起前**。在「决定重试」时点回调会在等待期
// ctx 取消时谎报重试（等待让位后并没有重试发生），故钩子挂在等待成功之后。
//
//	attempt = 刚失败的物理调用序号（1 基）；lastErr = 触发本次重试的错误。
//	全序耗尽时恰被调用 attempts-1 次；成功恢复时调用次数 = 恢复前的失败数。
func WithOnRetry(fn func(attempt int, lastErr error)) RateLimitRetryOption {
	return func(c *rateLimitRetryConfig) { c.onRetry = fn }
}

// WithOnExhausted 耗尽观测钩子（llm#209）：退避重试全部耗尽、失败定型时
// 回调一次——这是唯一能直接表达「LLM 不可用」的观测点（生产实弹 2026-10-22：
// 整场 LLM 全挂而事件/指标/告警三处全静默）。attempts = 总尝试次数（含首次），
// lastErr = 末次失败错误（与随后的 *RetryExhaustedError.Last 同源）。
func WithOnExhausted(fn func(attempts int, lastErr error)) RateLimitRetryOption {
	return func(c *rateLimitRetryConfig) { c.onExhausted = fn }
}

// WithRetryPredicate 重试谓词注入（llm#209）：覆盖缺省判定面（缺省 =
// ClassifyLLMError→IsRateLimit，写死文本/结构化 429 启发式）。宿主可按自己的
// 错误码体系收窄或放宽重试面。nil = 缺省判定（行为与今日逐字一致）。
func WithRetryPredicate(fn func(error) bool) RateLimitRetryOption {
	return func(c *rateLimitRetryConfig) { c.predicate = fn }
}

// RetryExhaustedError 429 退避重试耗尽的类型化出口（llm#209）。此前耗尽错误
// 是普通 fmt 包装，下游 errors.As 判不出「失败已定型为瞬态耗尽」，节点级重试
// 与改派分类失效。errors.As(err, *RetryExhaustedError) 判耗尽；
// errors.Is 穿透 Unwrap 链直达末次原始错误；Retryable() 鸭子契约（不引入任何
// 包依赖）供 toolfail 类分类器读「瞬态」。
type RetryExhaustedError struct {
	// Attempts 总尝试次数（含首次）——重试次数 = Attempts-1。
	Attempts int
	// Last 末次失败错误（耗尽时最后一次物理调用的错误）。
	Last error
}

// Error 文案与既有 fmt 包装形态逐字同族（llm#209 前："llm: 429 退避重试
// N 次仍限流: <lastErr>"），只多出类型化判定面，不换口径。
func (e *RetryExhaustedError) Error() string {
	return fmt.Sprintf("llm: 429 退避重试 %d 次仍限流: %v", e.Attempts-1, e.Last)
}

// Unwrap errors.Is 可穿透到末次原始错误（现有边界不变）。
func (e *RetryExhaustedError) Unwrap() error { return e.Last }

// Retryable 耗尽 = 瞬态失败的定型（鸭子契约，不引入任何包依赖）。
func (e *RetryExhaustedError) Retryable() bool { return true }

type rateLimitRetryConfig struct {
	attempts   int
	base, ceil time.Duration
	// jitterRatio 初始 -1（哨兵：未设置）——显式 0（关抖动）不得被缺省覆盖。
	jitterRatio float64
	// onRetry/onExhausted/predicate 观测与判定注入（llm#209），nil = 缺省。
	onRetry     func(attempt int, lastErr error)
	onExhausted func(attempts int, lastErr error)
	predicate   func(error) bool
}

func (c *rateLimitRetryConfig) fillDefaults() {
	if c.attempts <= 0 {
		c.attempts = defaultRetryAttempts
	}
	if c.base <= 0 {
		c.base = defaultRetryBaseDelay
	}
	if c.ceil <= 0 {
		c.ceil = defaultRetryMaxDelay
	}
	if c.jitterRatio < 0 || c.jitterRatio > 1 {
		c.jitterRatio = defaultRetryJitterRatio
	}
}

// RateLimitRetryModel 模型级限流退避装饰器（构建后只读，并发安全：无互斥状态）。
type RateLimitRetryModel struct {
	inner  einomodel.BaseChatModel
	cfg    rateLimitRetryConfig
	jitter mathx.Unstable
}

// 编译期断言：ADK/ReAct 绑工具经 ToolCallingChatModel.WithTools，装饰器必须
// 可派生（派生实例沿用同一退避配置，对齐 breaker/governor 装饰器家族）。
var _ einomodel.ToolCallingChatModel = (*RateLimitRetryModel)(nil)

// IsCallbacksEnabled 实现 eino components.Checker（oagentkit/llm#86）：转发
// 内层模型的判定——装饰器自身不发回调，不透传时 compose 会在节点外再注入
// 一轮回调，一次调用产出两条 usage 记录（预算/成本双计）。
func (m *RateLimitRetryModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.inner)
}

var _ components.Checker = (*RateLimitRetryModel)(nil)

// WithRateLimitRetry 给模型挂 429 退避装饰（RawModel 直驱路径一行套上）：
//
//	m := llm.WithRateLimitRetry(client.RawModel(),
//	    llm.WithRetryAttempts(5), llm.WithRetryJitterRatio(0.25))
//
// inner 为 nil 时原样返回。缺省曲线 = bianque 生产实证形态（5 次尝试、2s
// 基准、32s 封顶、0.25 单边向上抖动）；Retry-After 服务端建议经合理性钳制
// 后优先采信（仅限流失败采信，与 Client.generateRetry 同门控）。
func WithRateLimitRetry(inner einomodel.BaseChatModel, opts ...RateLimitRetryOption) einomodel.BaseChatModel {
	if inner == nil {
		return nil
	}
	cfg := rateLimitRetryConfig{jitterRatio: -1}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	cfg.fillDefaults()
	return &RateLimitRetryModel{
		inner:  inner,
		cfg:    cfg,
		jitter: mathx.NewUnstable(cfg.jitterRatio),
	}
}

// WithTools 工具绑定透传：绑定产物保持退避装饰（派生实例沿用同一配置，
// 与 Fit/Continue/Rate 同纪律）。内层不支持绑定时报错而非静默丢绑定。
func (m *RateLimitRetryModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	tc, ok := m.inner.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, errors.New("llm: 限流退避装饰的内层模型不支持工具绑定（ToolCallingChatModel）")
	}
	nb, err := tc.WithTools(tools)
	if err != nil {
		return nil, fmt.Errorf("llm: 限流退避装饰的内层模型绑定工具失败: %w", err)
	}
	return &RateLimitRetryModel{inner: nb, cfg: m.cfg, jitter: m.jitter}, nil
}

// Generate 429 退避重试：限流失败按指数曲线等待后重放同一次请求；非限流
// 失败原样上抛；ctx 取消立即让位。Retry-After sink 由 retryRateLimitCall
// 每轮物理调用前注入（llm#192）。
func (m *RateLimitRetryModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return retryRateLimitCall(ctx, m, func(ctx context.Context) (*schema.Message, error) {
		return m.inner.Generate(ctx, input, opts...)
	})
}

// Stream 建流前的 429 同样退避重试；流一旦建立即原样交还（不触碰流生命周期）。
func (m *RateLimitRetryModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return retryRateLimitCall(ctx, m, func(ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		return m.inner.Stream(ctx, input, opts...)
	})
}

// retryRateLimitCall 退避重试骨架（Generate/Stream 共用，泛型收口两类返回值，
// 成功拍直接返回真结果不重放）：attempt 1..attempts 执行 try；失败分类——
// 非限流/ctx 取消立即上抛；限流失败按同源退避曲线（waitBackoffGap，含
// Retry-After 取舍）等待后重试。耗尽上抛 *RetryExhaustedError（errors.Is 经
// Unwrap 链直达末次原始错误，llm#209）。
//
// Retry-After sink 每轮物理调用前注入全新实例（llm#192）：hint 的生命周期 =
// 单次失败。此前 sink 在环上游注入一次、整环复用——上一轮 429 带的
// Retry-After（如 300s）在下一轮「不带该头的 429」上仍被采信，本地曲线（4s
// 级）被陈旧整段窗口盖过，最坏单次 Generate 卡满 Σhint（仅 ctx 可打断）。
func retryRateLimitCall[T any](ctx context.Context, m *RateLimitRetryModel, try func(ctx context.Context) (T, error)) (T, error) {
	var lastErr error
	attemptCtx := ctx
	for attempt := 1; attempt <= m.cfg.attempts; attempt++ {
		if attempt > 1 {
			// 首拍等待 = base（与 Client.generateRetry 的 0 基 attempt 曲线同源：
			// waitBackoffGap 的 attempt 参数即 backoffDelay 的 1 基拍号）。
			// attemptCtx 是上一轮物理调用的 sink——只采信本次失败产生的建议
			if err := waitBackoffGap(attemptCtx, attempt-1, m.cfg.base, m.cfg.ceil, true, m.jitter); err != nil {
				var zero T
				return zero, err // ctx 取消原样上抛（此时不触发 OnRetry：等待让位后重试并未发生）
			}
			// OnRetry 时点 = 等待结束后、物理调用发起前（llm#209）：在「决定
			// 重试」时点回调会在等待期 ctx 取消时谎报（见上）。
			if m.cfg.onRetry != nil {
				m.cfg.onRetry(attempt-1, lastErr)
			}
		}
		attemptCtx = WithRetryAfterSink(ctx)
		out, err := try(attemptCtx)
		if err == nil {
			return out, nil
		}
		if ctx.Err() != nil {
			var zero T
			return zero, ctx.Err() // 调用方中止：结果未知，不再烧后续尝试
		}
		if !m.retryable(err) {
			var zero T
			return zero, err // 非限流失败不归本装饰器管
		}
		lastErr = err
	}
	// 耗尽：失败定型（llm#209）——钩子恰一次，错误类型化（errors.As 可判、
	// errors.Is 穿透 Unwrap 到末次原始错误、Retryable 自报瞬态）。
	if m.cfg.onExhausted != nil {
		m.cfg.onExhausted(m.cfg.attempts, lastErr)
	}
	var zero T
	return zero, &RetryExhaustedError{Attempts: m.cfg.attempts, Last: lastErr}
}

// retryable 重试判定：注入谓词优先（llm#209，宿主按自己的错误码体系裁决），
// 缺省 ClassifyLLMError→IsRateLimit（结构化状态码 → 文本 marker）。
func (m *RateLimitRetryModel) retryable(err error) bool {
	if m.cfg.predicate != nil {
		return m.cfg.predicate(err)
	}
	return ClassifyLLMError(err).IsRateLimit
}
