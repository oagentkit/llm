// Package port 是 oagentkit/llm 的**窄接口层**——本仓不依赖 eino 的那一半。
//
// # 为什么需要它
//
// 本仓全部对外能力原先都长在 eino 的类型上：
//
//	func NewClient(m model.BaseChatModel, modelName string, budget TokenAccountant) *Client
//	func (c *Client) Generate(ctx, stage, msgs []*schema.Message) (*schema.Message, error)
//
// 非 eino 生态的宿主想接一个**单方法**窄接口（如
// `Complete(ctx, req LLMRequest) (string, error)`），就只能自己重写一遍：
// 拼请求体、解析响应、判错误码、算超时、处理 JSON 模式。代价不只是代码量，
// 还有**边界易错**——HTTP 状态码判错就会静默把失败当成功。
//
// 本包把这些能力收在一处：**降级链 / 任务级预算 / 成本记账**在窄接口形态下
// 同样可用，且与 eino 原生路径**共用同一份实现**（见下）。
//
// # 依赖面
//
// 本包**不引入 eino**（用 `go list -deps ./port` 可验证）。它只依赖：
// 标准库、oekit/concurrency（熔断）、oagentkit/textx（token 估算）。
//
// # 怎么选：窄接口 vs eino 原生
//
//   - 宿主已在 eino 生态（要 ToolCall / 多模态 / 流式 / 结构化输出到 struct）：
//     直接用根包 llm.NewClient / llm.NewResilient（能力更全，含 JSON 模式、
//     分阶段路由、context 窗口 fitInput）。
//   - 宿主不在 eino 生态（只要「一句话进、一句话出」，或不想把 eino 带进依赖图）：
//     用本包。宿主自己实现 Completer（拼 HTTP 请求），本包负责降级、预算、成本。
//
// 两条路径的**预算与成本口径完全一致**：本包与根包共用同一份 Budget / CostTracker /
// PriceTable 实现（见 budget.go / cost.go / pricing.go 的说明），不会出现两套算账。
package port

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oagentkit/textx/textutil"
)

// Request 一次窄接口调用的请求。
//
// 刻意做窄：只保留「非 eino 宿主也需要」的字段——宿主自己拼请求体，
// 本包不越界替宿主决定 prompt 模板或协议细节。
type Request struct {
	// Prompt 用户输入。
	Prompt string
	// System 系统提示（可选）。
	System string
	// JSON 要求模型输出 JSON。**只是意图透传**：本层不解析 JSON
	// （eino 路径的 GenerateJSON 才是「解析进 struct」），宿主按需自行反序列化。
	JSON bool
	// Stage 阶段标签（预算/成本按阶段归集；与 eino 路径的 stage 语义一致）。
	Stage string
	// MaxTokens 输出上限（0 = 不限）。
	MaxTokens int
}

// Completer 窄接口：**一个方法**完成一次调用。
//
// 名字与下游一致（agent-memory 的 ports.LLM: Complete(ctx, req) (string, error)），
// 宿主实现它即可接进降级链。本身不引任何 eino 类型。
type Completer interface {
	Complete(ctx context.Context, req Request) (string, error)
}

// Usage 一次调用的 token 用量。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// TokenCounter 精确 token 计量口。
//
// 默认口径是**估算**（textutil.EstTokensCJK，与 eino 路径的 estimateTokens 同源）。
// 宿主若能从 provider 回传 usage，注入本口即可换成精确值——
// 链在 Completer 返回后**紧接着**调用它，所以宿主在同一调用内暂存 usage 是安全的。
type TokenCounter func(req Request, resp string) Usage

// Classifier 错误分类：返回该错误是否值得重试（true）或应立刻切下一个（false）。
//
// 默认口径：除 ctx 取消/超时外都算可重试。宿主应按自己的错误形态覆盖它——
// 本包提供 ClassifyStatus（按 HTTP 状态码）覆盖最常见的 OpenAI 兼容场景，
// 免得每个宿主各写一遍、各判错一遍。
type Classifier func(err error) bool

// ErrBudgetExhausted 任务 token 预算已耗尽（调用前拦下）。
var ErrBudgetExhausted = errors.New("port: 任务 token 预算已耗尽")

// ErrCircuitOpen 熔断打开：该环节暂不可用，已切向下一环。
var ErrCircuitOpen = errors.New("port: 熔断打开")

// ErrEmptyChain 链上没有任何环节。
var ErrEmptyChain = errors.New("port: 降级链为空")

// Entry 链上的一环：名字 + 窄接口实现。
type Entry struct {
	// Name 环节名：熔断分桶键 + 成本记录里的模型名。
	Name string
	// Completer 窄接口实现。
	Completer Completer
}

type chainEntry struct {
	name string
	c    Completer
}

// Chain 窄接口形态的降级链。
//
// 装配后**只读**，可并发使用。
type Chain struct {
	entries  []chainEntry
	budget   TokenAccountant
	tracker  *CostTracker
	breakers *Breakers
	attempts int
	base     time.Duration
	maxDelay time.Duration
	classify Classifier
	count    TokenCounter
}

// ChainOption 链装配选项。
type ChainOption func(*Chain)

// NewChain 创建窄接口降级链。
//
// 链上环节的**先后顺序 = WithCompleter 的调用顺序**（先注册者优先）。
// 不带任何 WithCompleter 时链为空，Complete 返回 ErrEmptyChain。
//
// 熔断缺省由 NewBreakers 铺底（DefaultBreakerTrip=3 连败 / DefaultBreakerCooldown=5min，
// 与 eino 路径 ResilientConfig.fillDefaults 同源）；换阈值用 WithBreaker 传
// 自定义 NewBreakers(...)（oagentkit/llm#165）。
func NewChain(opts ...ChainOption) *Chain {
	c := &Chain{
		attempts: 1,
		base:     2 * time.Second,
		maxDelay: 30 * time.Second,
		classify: defaultClassifier,
		breakers: NewBreakers(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithCompleter 往链上加一环（name 用于熔断分桶与成本记账的模型名）。
func WithCompleter(name string, c Completer) ChainOption {
	return func(ch *Chain) {
		ch.entries = append(ch.entries, chainEntry{name: name, c: c})
	}
}

// WithBudget 绑定任务级预算：预算耗尽时**在调用前**拦下（事后扣减等于没有预算）。
func WithBudget(b TokenAccountant) ChainOption {
	return func(c *Chain) { c.budget = b }
}

// WithTracker 绑定成本追踪器：每次调用按价表计价并记账。
// 价表里查不到的模型记为「未定价」（CostRecord.PriceStatus = PriceUnpriced），
// **不记 0**——0 成本与「免费模型」不可区分。
func WithTracker(t *CostTracker) ChainOption {
	return func(c *Chain) { c.tracker = t }
}

// WithBreaker 绑定熔断器（按环节名分桶）。nil = 未设置（保留 NewChain 装的
// 缺省 NewBreakers，与 WithClassifier 同纪律）——此前 nil 直接覆盖清掉缺省，
// 首次 Complete 在 c.breakers.Allow 上空指针 panic（oagentkit/llm#88）。
// 本选项没有「显式禁用熔断」语义（Breakers 不存在禁用态）。
func WithBreaker(b *Breakers) ChainOption {
	return func(c *Chain) {
		if b != nil {
			c.breakers = b
		}
	}
}

// WithRetries 每个环节的重试次数（不含首次），默认 1（即不重试）。
func WithRetries(n int) ChainOption {
	return func(c *Chain) {
		if n > 0 {
			c.attempts = n + 1
		}
	}
}

// WithBackoff 重试退避：base 起倍，max 封顶。默认 2s / 30s。
func WithBackoff(base, max time.Duration) ChainOption {
	return func(c *Chain) {
		if base > 0 {
			c.base = base
		}
		if max > 0 {
			c.maxDelay = max
		}
	}
}

// WithClassifier 覆盖错误分类（默认：除 ctx 取消/超时外都算可重试）。
func WithClassifier(f Classifier) ChainOption {
	return func(c *Chain) {
		if f != nil {
			c.classify = f
		}
	}
}

// WithTokenCounter 覆盖 token 计量口径（默认按 textutil.EstTokensCJK 估算）。
func WithTokenCounter(f TokenCounter) ChainOption {
	return func(c *Chain) { c.count = f }
}

// budgetGate 预算闸（单源文案）：预算耗尽时在调用前拦下——事后扣减等于没有
// 预算。budget 未绑定时闸常开。
func (c *Chain) budgetGate() error {
	if c.budget == nil || c.budget.Remaining() > 0 {
		return nil
	}
	return fmt.Errorf("%w（已用 %d）", ErrBudgetExhausted, c.budget.Used())
}

// Complete 走降级链完成一次调用。
//
// 顺序：**预算前置判定 → 逐环节尝试（每环节内部可重试）→ 失败切下一环节**。
// 预算在**两处**复查（llm#186，对齐 eino 路径 Resilient.stepChain 的逐 provider
// 复查）：整次调用入口 + 每环节入口——第一环烧穿预算（本环失败面记账、并发
// 任务共烧同一任务级预算）后，第二、第三环不再盲发；此前只在入口判一次，
// 预算短路在窄接口路径退化为「只拦第一次调用」。
//
// 成功时：预算累计消耗、成本按价表记账（价表未收录的模型记为「未定价」，不记 0）。
// 失败时：返回最后一个错误；ctx 取消/超时时**原样上抛**（用户主动中止不该
// 把模型推向熔断，也不该被当成模型故障）。
func (c *Chain) Complete(ctx context.Context, req Request) (string, error) {
	if len(c.entries) == 0 {
		return "", ErrEmptyChain
	}
	if err := c.budgetGate(); err != nil {
		return "", err
	}

	var lastErr error
	for _, e := range c.entries {
		// ctx 已取消/超时 → 不再发起调用（拿一个死 context 去请求模型没有意义）。
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// 预算逐环节复查（llm#186）：越早短路越省钱
		if err := c.budgetGate(); err != nil {
			return "", err
		}
		gen, allowed := c.breakers.Allow(ctx, e.name)
		if !allowed {
			// 熔断拒绝放行 → 切下一环。
			lastErr = fmt.Errorf("%w: %s", ErrCircuitOpen, e.name)
			continue
		}
		out, err := c.attempt(ctx, e, req, gen)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			// 用户主动中止：原样上抛，不当作模型故障。
			return "", err
		}
	}
	if lastErr == nil {
		lastErr = ErrEmptyChain
	}
	return "", lastErr
}

// attempt 在一个环节内完成调用（可重试）。
//
// 熔断记账口径与 Resilient.tryProvider 对齐（llm#141）：一次 Allow 放行
// （含内部 N 次物理重试）只在收口处报**一次** Failure/Success/Abandon——
// 此前每次物理重试都报 Failure，连续失败阈值被单个逻辑请求整体烧穿
// （WithConsecutiveFailures(3) + WithRetries(2) 下一次 Complete 即熔断），
// 且 WithRetries 文档从未声明「重试会加速熔断」。
//
// 每轮物理调用失败都做失败面记账（llm#191，与 eino 路径 Client.accountFailed
// 同口径）；环内重试前复查预算（llm#186）——重试间隙预算被烧穿（本环失败面
// 记账、并发任务共烧）则不再发起下一轮物理调用。
func (c *Chain) attempt(ctx context.Context, e chainEntry, req Request, gen uint64) (string, error) {
	var lastErr error
	for i := range c.attempts {
		out, err := e.c.Complete(ctx, req)
		if err == nil {
			c.breakers.Success(e.name, gen)
			c.account(e.name, req, out)
			return out, nil
		}
		// 失败面记账（llm#191）：请求已发出（超时/中断/429/5xx——provider
		// 大概率已受理计费），按估算记 prompt 侧、completion 记 0。此前只在
		// 成功时记账，预算与成本对失败面系统性偏低。
		c.accountFailed(e.name, req)
		lastErr = err
		if ctx.Err() != nil {
			// 用户主动中止：放弃这次放行，不计失败统计（否则连续取消会误熔断）。
			c.breakers.Abandon(e.name, gen)
			return "", err
		}
		if !c.classify(err) {
			// 不可重试 → 真实失败，报一次 Failure 切下一环。
			c.breakers.Failure(ctx, e.name, gen)
			return "", err
		}
		if i+1 < c.attempts {
			// 环内重试前复查预算（llm#186）：本环物理失败已发生（报一次真实
			// Failure 收口这条放行），预算耗尽则不再烧下一轮。
			if err := c.budgetGate(); err != nil {
				c.breakers.Failure(ctx, e.name, gen)
				return "", err
			}
			if !sleep(ctx, c.delay(i)) {
				// 退避等待被 ctx 取消：失败已发生（非调用中取消），按一次
				// 真实失败记账后原样上抛——与旧实现（循环内先报 Failure 再
				// 睡）记账等价，只是收口到一次。
				c.breakers.Failure(ctx, e.name, gen)
				return "", ctx.Err()
			}
		}
	}
	// 重试耗尽：整次领取报一次 Failure（解除半开探测占位含在内）。
	c.breakers.Failure(ctx, e.name, gen)
	return "", lastErr
}

// account 记账：预算累计 + 成本记录。
func (c *Chain) account(name string, req Request, resp string) {
	u := c.usage(req, resp)
	if c.budget != nil {
		c.budget.Add(u.PromptTokens + u.CompletionTokens)
	}
	if c.tracker != nil {
		c.tracker.RecordAuto(name, req.Stage, u.PromptTokens, u.CompletionTokens)
	}
}

// accountFailed 失败面记账（llm#191）：失败拿不到宿主 usage，按估算记 prompt
// 侧、completion 记 0——CostRecord 的 completion=0 即「部分记录」标记（只有
// 成功路径才有 completion）。估算口径与 usage() 同源；宿主注入 TokenCounter
// 时以其 req 侧计量为准。与成功路径 account 按物理调用互斥，不双计。
func (c *Chain) accountFailed(name string, req Request) {
	if c.budget == nil && c.tracker == nil {
		return
	}
	prompt := c.usage(req, "").PromptTokens
	if c.budget != nil {
		c.budget.Add(prompt)
	}
	if c.tracker != nil {
		c.tracker.RecordAuto(name, req.Stage, prompt, 0)
	}
}

// usage 取本次调用的 token 用量（精确计量优先，否则估算）。
func (c *Chain) usage(req Request, resp string) Usage {
	if c.count != nil {
		return c.count(req, resp)
	}
	return Usage{
		PromptTokens:     estimateTokens(req.System) + estimateTokens(req.Prompt),
		CompletionTokens: estimateTokens(resp),
	}
}

// delay 第 i 次重试前的退避时长（指数，max 封顶）。
func (c *Chain) delay(i int) time.Duration {
	d := c.base
	for range i {
		d *= 2
		if d >= c.maxDelay {
			return c.maxDelay
		}
	}
	if d > c.maxDelay {
		return c.maxDelay
	}
	return d
}

// estimateTokens 无 usage 回传时的 token 估算——textutil.EstTokensCJK 单源
// （与 eino 路径 client.go 的 estimateTokens 同源，两条路径口径一致）。
func estimateTokens(s string) int { return textutil.EstTokensCJK(s) }

// sleep 可被 ctx 打断的等待。
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// defaultClassifier 默认错误分类：除 ctx 取消/超时外都算可重试。
func defaultClassifier(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// StatusError 宿主 transport 的错误形态：带 HTTP 状态码。
//
// 存在的意义：把「HTTP 状态码 → 要不要重试」这个**最容易判错**的判定
// 收进本包一次，宿主不必各写一遍（判错的后果是静默把失败当成功）。
type StatusError struct {
	// Status HTTP 状态码（0 = 非 HTTP 错误）。
	Status int
	// Err 底层错误。
	Err error
}

func (e *StatusError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("port: HTTP %d", e.Status)
	}
	return fmt.Sprintf("port: HTTP %d: %v", e.Status, e.Err)
}

// Unwrap 支持 errors.Is / errors.As 穿透到底层错误。
func (e *StatusError) Unwrap() error { return e.Err }

// ClassifyStatus 按 HTTP 状态码判定是否值得重试：
// 429（限速）与 5xx（服务端故障）值得；其余 4xx（请求本身有问题，重试无益）不值得。
//
// 它同时是 Classifier——直接传给 WithClassifier 即可。
func ClassifyStatus(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if se, ok := errors.AsType[*StatusError](err); ok {
		switch {
		case se.Status == 429:
			return true
		case se.Status >= 500:
			return true
		case se.Status >= 400:
			return false
		}
	}
	return true
}
