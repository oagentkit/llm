// Package llm LLM 客户端栈（eino BaseChatModel 之上），本包唯一 package doc。
// 文件族分工：
//   - client/errors/config：客户端薄封装——生成失败指数退避+jitter 重试（429 退避
//     下限提高）、JSON 输出解析失败带错误回喂重试、token 记账（优先响应 usage，
//     缺省 CJK 感知估算（EstTokensCJK））、fitInput 输入自守恒、可选 token bucket 限速
//   - provider/openai：Provider 抽象与多后端 + FallbackChain 降级链
//   - resilient/failover：弹性客户端（降级矩阵/熔断/预算短路）+ 裸模型路径的
//     FailoverModel 装饰器
//   - budget/cost/usage：预算、成本、完整用量采集三套记账
//   - stage_router：按业务阶段路由模型
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/obsx"
	"github.com/oagentkit/textx/textutil"
	"github.com/oekit/pkg/jsonrepair"
	"github.com/oekit/pkg/mathx"
)

// Waiter 限速最小面：本包对限速器的全部消费就是 Wait(ctx)（Client 与
// wrap.RateModel 的调用点各一处）。
//
// 拆出单方法接口的原因：完整四方法面（Allow/AllowN/Reserve/Wait）要求宿主
// 自带限速器时补齐三个**永远不会被调用**的方法——其中 Reserve 的语义映射
// 还没有合理答案（本接口「返回 0=立即可用」没有拒绝语义，x/time/rate 的
// Reserve 有 ok=false 路径，两套语义对不齐）。Go 接口隐式满足：oekit
// ratelimit.Limiter 的实现（TokenBucket/SlidingWindow）与任何自带 Wait 的
// 限速器都自然满足本接口，字段类型收窄成消费面是纯放宽、零破坏。
type Waiter interface {
	// Wait 等待直到获取令牌或 ctx 取消；ctx 取消返回 ctx.Err()。
	Wait(ctx context.Context) error
}

// Client LLM 客户端（带重试、限速、预算、fitInput）。
type Client struct {
	Model     model.BaseChatModel
	ModelName string

	// 模型窗口（token，输入+输出合计）与单次输出预留。
	ContextTokens   int
	MaxOutputTokens int

	// OnUsage 每次调用后的 token 记账回调（stage, promptTokens, completionTokens）。
	OnUsage func(stage string, prompt, completion int)
	// OnUsageDetailed 每次调用后的完整用量回调（含前缀缓存命中量等）。
	//
	// 与 OnUsage 并行触发、两者可同置：OnUsage 保持旧签名不破坏；需要 cached 的
	// 消费方（成本缓存折扣、命中率观测）用本回调。UsageRecord.CachedTokens 是
	// 命中前缀缓存的 prompt token（DeepSeek/MiMo 等端点的 PromptTokenDetails）。
	//
	// 与 OnUsage 同属「Client 侧遥测出口」并**同样参与防重**（llm#132）：任一
	// 出口配置时 generateRetry 打「Client 已记账」标记，NewUsageHandler 对同一
	// 次物理调用跳过——只配本回调不会同 callbacks 侧双计。
	OnUsageDetailed func(stage string, rec UsageRecord)
	// Budget 任务 token 预算累计器（可 nil）。
	Budget TokenAccountant
	// Limiter 全局 LLM 请求限速器（Waiter 最小面：仅需 Wait(ctx)；标准构造
	// ratelimit.NewTokenBucketFloat(rps, burst)，SlidingWindow 等实现可插，
	// 宿主自带限速器只需实现 Wait）；nil = 不限速。
	Limiter Waiter

	MaxRetries int           // 最大尝试次数（含首次），默认 3
	BaseDelay  time.Duration // 指数退避基准，默认 2s
	MaxDelay   time.Duration // 退避上限，默认 30s

	// AttemptTimeout 单次尝试独立限时（0=不限，默认；llm#84）。单客户端路径
	// 对齐 Resilient 链的 per-provider AttemptTimeout：挂死的 provider 连接
	// （TLS 黑洞、网关半开、流式静默断流）由本限时兜底，而不是把整个重试
	// 循环的时间预算耗在第一次尝试上。每次物理尝试各自计时——刻意不覆盖
	// 整个重试循环（首次尝试耗满 deadline 后重试全部形同虚设）。
	// 建议量级 60–120s；最坏挂钟预算口径：≈ MaxRetries×AttemptTimeout + Σ退避。
	AttemptTimeout time.Duration
}

// NewClient 创建 LLM 客户端。
func NewClient(m model.BaseChatModel, modelName string, budget TokenAccountant) *Client {
	return &Client{
		Model: m, ModelName: modelName, Budget: budget,
		MaxRetries: 3, BaseDelay: 2 * time.Second, MaxDelay: 30 * time.Second,
	}
}

// RawModel 返回底层 eino 模型（Generator 接口实现）。
func (c *Client) RawModel() model.BaseChatModel { return c.Model }

// UsedTokens 返回累计消耗 token 数（无预算绑定时返回 0）。
func (c *Client) UsedTokens() int {
	if c.Budget != nil {
		return c.Budget.Used()
	}
	return 0
}

// BoundBudget 返回绑定的预算累计器（可 nil）。BudgetHolder 接口实现。
func (c *Client) BoundBudget() TokenAccountant { return c.Budget }

// estimateTokens 无 usage 回传时的 token 估算——textutil.EstTokensCJK 单源
// （CJK 感知：Han/Kana/全角计 2 单位、tokens≈units/3+1，中文为主的提示词/
// 会话历史不再系统性低估；v0.11.2 起 v0.10.40 的 EstTokensOf 切换为 CJK
// 口径——英文估算略升为保守方向，精确计量始终归 provider usage 回传）。
func estimateTokens(s string) int { return textutil.EstTokensCJK(s) }

// estimateMsgTokens 无 usage 回传时的整消息估算面（llm#139）：正文之外，
// ToolCalls（工具名 + 参数 JSON——大入参工具调用只算正文会系统性低估，
// 预算短路形同虚设）与三组多模态字段的文本部件同样计入。口径对齐
// textx/compact 的 msgTokens（同 org 两套估算不许分叉：彼处同样正文 +
// ToolCalls 双计），多模态部件按文本计——图片/音频 URL 的真实 token 由
// provider usage 回传兜底，本地估算只做保守下界。字段面与 MsgRuneCount
// （rune 口径，#187 起裁剪面共用）单源对齐。
func estimateMsgTokens(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		total += estimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += estimateTokens(tc.Function.Name) + estimateTokens(tc.Function.Arguments)
		}
		for _, part := range m.MultiContent {
			total += estimateTokens(part.Text)
		}
		for _, part := range m.UserInputMultiContent {
			total += estimateTokens(part.Text)
		}
		for _, part := range m.AssistantGenMultiContent {
			total += estimateTokens(part.Text)
		}
	}
	return total
}

// MsgRuneCount 单消息的 rune 口径（#187）：正文 Content 之外，ToolCalls（工具
// 名 + 参数 JSON——大入参工具调用只算正文会系统性低估，窗口治理形同虚设）与
// 三组多模态字段的文本部件同样计入。字段面与 estimateMsgTokens（token 口径）
// 单源对齐：记账与裁剪（fitInputFor / wrap.FitMessages 的 runeTotal/msgRunes）
// 两条出口同一口径——此前「记账算了、裁剪没算」，tool-heavy 会话的窗口治理
// 失明。多模态非文本部件（图片/音频）的真实 token 由 provider usage 回传兜底，
// 本计数只做保守下界。
func MsgRuneCount(m *schema.Message) int {
	if m == nil {
		return 0
	}
	n := utf8.RuneCountInString(m.Content)
	for _, tc := range m.ToolCalls {
		n += utf8.RuneCountInString(tc.Function.Name) + utf8.RuneCountInString(tc.Function.Arguments)
	}
	for _, part := range m.MultiContent {
		n += utf8.RuneCountInString(part.Text)
	}
	for _, part := range m.UserInputMultiContent {
		n += utf8.RuneCountInString(part.Text)
	}
	for _, part := range m.AssistantGenMultiContent {
		n += utf8.RuneCountInString(part.Text)
	}
	return n
}

// MsgsRuneCount 消息序列的 rune 总量（MsgRuneCount 逐条求和）。
func MsgsRuneCount(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		total += MsgRuneCount(m)
	}
	return total
}

func (c *Client) account(stage string, in []*schema.Message, out *schema.Message) {
	var p, comp, cached int
	if out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		u := out.ResponseMeta.Usage
		p, comp = u.PromptTokens, u.CompletionTokens
		// 前缀缓存命中量（CachedTokens ⊆ PromptTokens）：只在真实 usage 路径取，
		// 估算路径无缓存概念、留 0。
		cached = u.PromptTokenDetails.CachedTokens
	} else {
		p = estimateMsgTokens(in)
		if out != nil {
			comp = estimateMsgTokens([]*schema.Message{out})
		}
	}
	if c.OnUsage != nil {
		c.OnUsage(stage, p, comp)
	}
	if c.OnUsageDetailed != nil {
		c.OnUsageDetailed(stage, UsageRecord{
			Model:            c.ModelName,
			PromptTokens:     p,
			CachedTokens:     cached,
			CompletionTokens: comp,
		})
	}
	if c.Budget != nil {
		c.Budget.Add(p + comp)
	}
}

// Generate 带重试的普通生成（指数退避 + jitter）。
// 确定性失败（鉴权/权限/上下文超限）不重试；429 退避下限提高后值得等
// （单客户端无备选可切）。stage 同时注入 ctx（obsx.WithStage）——eino
// callbacks handler 可读到业务阶段。
func (c *Client) Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error) {
	ctx = obsx.WithStage(ctx, stage)
	msgs = copyMsgs(msgs) // fitInput 原地替换元素，不能污染调用方的切片
	if err := c.fitInput(msgs); err != nil {
		return nil, err
	}
	return c.generateFitted(ctx, stage, msgs)
}

// generateFitted 假定 msgs 已是副本且完成 fitInput（Resilient.prepare 路径复用，
// 免 JSON/降级链上再 copy+fit 一遍）。
func (c *Client) generateFitted(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error) {
	pol := c.retryPolicy()
	out, attempts, err := c.generateRetry(ctx, stage, msgs, pol)
	if err != nil {
		return nil, fmt.Errorf("llm: %s 调用失败（已重试 %d 次）: %w", stage, attempts-1, err)
	}
	return out, nil
}

// retryPolicy 由 Client 字段推导单模型重试参数（缺省落位只算一次）。
func (c *Client) retryPolicy() retryPolicy {
	maxRetries := c.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	base := c.BaseDelay
	if base <= 0 {
		base = 2 * time.Second
	}
	ceil := c.MaxDelay
	if ceil <= 0 {
		ceil = 30 * time.Second
	}
	return retryPolicy{
		maxAttempts:    maxRetries,
		base:           base,
		ceil:           ceil,
		chainFallback:  false,            // 单客户端无备选模型，429 值得退避等待
		attemptTimeout: c.AttemptTimeout, // 0=不限；单次尝试独立限时（llm#84），
		// 此前该字段恒 0——attemptCtx 原样透传，挂死 provider 只能靠外层 ctx
	}
}

// retryPolicy 单模型重试策略（Client.Generate 与 Resilient.tryOneProvider 共用
// 骨架的参数——重试核心只有一份实现，文案/策略不再漂移）。
type retryPolicy struct {
	maxAttempts int // 总尝试次数（含首次）
	base, ceil  time.Duration
	// chainFallback true=429 与确定性失败直接跳出（上层降级链有备选模型可切）；
	// false=仅确定性失败跳出，429 值得退避等待（单客户端无备选）。
	chainFallback bool
	// attemptTimeout 单次尝试独立限时（0=不限）。不能覆盖整个重试循环——
	// 首次尝试耗满 deadline 后重试全部形同虚设。
	attemptTimeout time.Duration
}

// generateRetry 单模型重试核心（全包唯一实现）：限速 → 截断后提升 MaxTokens
// （一次）→ Generate → finish_reason=length 转错误并记账 → 按 ClassifyLLMError
// 决定重试。ctx 取消原样上抛。
// 返回值 attempts 为实际执行的物理尝试数（错误文案「已重试 N 次」的真实计数，
// 不再按 policy 上限谎报）。
func (c *Client) generateRetry(ctx context.Context, stage string, msgs []*schema.Message, pol retryPolicy) (*schema.Message, int, error) {
	// Client 侧已配置任一遥测出口（OnUsage / OnUsageDetailed，llm#132 起两者
	// 同口径）时打标记：NewUsageHandler 检测到标记跳过发射——同一次物理调用
	// 的 Generate 路径与 callbacks 路径只记一次遥测。
	// Budget 不算遥测出口：只配 Budget（未配 OnUsage/Tracker）时不打标记，
	// 否则 callbacks 侧的用量遥测会被预算静默吞掉（第六轮审计）
	if c.OnUsage != nil || c.OnUsageDetailed != nil {
		ctx = withClientAccounting(ctx)
	}
	// Retry-After sink（批次四十五）：传输层捕获 429 服务端建议，退避取舍时优先采信
	ctx = WithRetryAfterSink(ctx)
	var lastErr error
	var lastHint RetryHint
	// 提升后的输出上限（0 = 未提升）。粘性记在「当前生效参数」上而不是
	// 「是否尝试过」（llm#142）：此前 opts 逐轮重建、只在截断 hint 的下一轮
	// 携带 boost——提升轮撞瞬态失败后重试静默退回原上限再截断，最终被上层
	// 读成「提升后仍截断」而放弃。提升后每轮恒携带，直到提升轮真的再截断
	// 才判「提升无效」收手。
	boostTokens := 0
	attempts := 0
	for attempt := range pol.maxAttempts {
		if attempt > 0 {
			if !sameModelRetryDecision(pol.chainFallback, lastHint, lastErr) {
				break
			}
			if err := waitRetryGap(ctx, attempt, pol, lastHint); err != nil {
				return nil, attempts, err
			}
		}
		if lastHint.IsTruncated {
			// 提升后仍截断（或无 MaxOutputTokens 可提升）：同参数重试注定再
			// 截断，白烧一次钱——就此打住，交上层（降级链切换/调用方处置）
			if boostTokens > 0 || c.MaxOutputTokens <= 0 {
				break
			}
			boostTokens = c.MaxOutputTokens * 3 / 2
			// 提升输出上限的那轮按新上限重算输入预算再裁剪一次（llm#83）：
			// 输入按 1 倍预留裁剪过，1.5 倍输出上限 + 原输入可能 > 窗口——
			// 二次尝试以 context too long 收场，还会被 ClassifyLLMError 判成
			// 确定性失败，在降级链上白烧一次模型切换
			c.fitInputFor(boostTokens, msgs)
		}
		// boost 粘性（llm#142）：提升后的每一轮（含非截断失败的续命重试轮）
		// 都按提升值携带，不许静默回落原上限。
		var opts []model.Option
		if boostTokens > 0 {
			opts = append(opts, model.WithMaxTokens(boostTokens))
		}
		attempts = attempt + 1
		out, err := c.oneAttempt(ctx, stage, msgs, pol.attemptTimeout, opts)
		if err == nil {
			return out, attempts, nil
		}
		lastErr = err
		lastHint = ClassifyLLMError(err) // 每轮只分类一次，复用于重试决策/退避/截断提升
	}
	return nil, attempts, lastErr
}

// sameModelRetryDecision 重试决策：chainFallback（降级链有备选）时仅"值得在同模型
// 上重试"的错误继续（429 与确定性失败直接跳出，切换下一模型）；单客户端按
// RetryHint.Retryable。
func sameModelRetryDecision(chainFallback bool, hint RetryHint, err error) bool {
	if chainFallback {
		return sameModelRetryable(hint, err)
	}
	return hint.Retryable
}

// waitRetryGap 重试前退避等待：本地指数曲线 + 服务端 Retry-After 建议取舍。
// ctx 取消原样上抛。
func waitRetryGap(ctx context.Context, attempt int, pol retryPolicy, hint RetryHint) error {
	return waitBackoffGap(ctx, attempt, pol.base, pol.ceil, hint.IsRateLimit, retryJitter)
}

// waitBackoffGap 退避等待唯一实现（Client.generateRetry 与模型级
// RateLimitRetry 装饰器共用——曲线/Retry-After 取舍两处不再漂移，llm#100）：
// 本地指数曲线（抖动器可配）+ 服务端建议钳制采信。ctx 取消原样上抛。
func waitBackoffGap(ctx context.Context, attempt int, base, ceil time.Duration, isRateLimit bool, jitter mathx.Unstable) error {
	delay := backoffDelayJittered(attempt, base, ceil, isRateLimit, jitter)
	// 服务端建议优先（合理性钳制：≤5min 或短于本地曲线才采信——长限流交降级链）。
	// 只在本次失败确为限流时采信：sink 不随尝试清除，429 之后的 500 若也读
	// sink，会对非限流错误白等陈旧的整段限流窗口（第六轮审计）
	if isRateLimit {
		if after := RetryAfterFrom(ctx); after > 0 {
			delay = SelectRetryDelay(delay, after)
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

// oneAttempt 单次物理调用：限速等待 → 独立限时 Generate → 记账；
// finish_reason=length 转为截断错误（供重试决策/截断提升）。
func (c *Client) oneAttempt(ctx context.Context, stage string, msgs []*schema.Message, attemptTimeout time.Duration, opts []model.Option) (*schema.Message, error) {
	if c.Limiter != nil {
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("llm: %s 限速等待取消: %w", stage, err)
		}
	}
	// 每轮独立 cancel：defer 会堆到函数尾，多轮重试囤积计时器/ctx
	attemptCtx := ctx
	var cancel context.CancelFunc
	if attemptTimeout > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, attemptTimeout)
	}
	out, err := c.Model.Generate(attemptCtx, msgs, opts...)
	if cancel != nil {
		cancel()
	}
	if err != nil {
		return nil, err
	}
	c.account(stage, msgs, out)
	if out.ResponseMeta != nil && out.ResponseMeta.FinishReason == "length" {
		// Usage 是否存在取决于 Provider 实现（部分兼容端点缺失），不能裸解引用
		ct := "?"
		if out.ResponseMeta.Usage != nil {
			ct = fmt.Sprint(out.ResponseMeta.Usage.CompletionTokens)
		}
		return nil, fmt.Errorf("llm: %s 输出被截断（finish_reason=length, completion_tokens=%s）",
			stage, ct)
	}
	return out, nil
}

// retryJitter 退避抖动器：单边向上 50%（[d, 1.5d)），与此前手搓 delay+=rand[0,d/2)
// 逐位等价；对称抖动会把退避压短成更密集的重试风暴，故不用 Around。
var retryJitter = mathx.NewUnstable(0.5)

// backoffDelay 指数退避 + jitter（Client 与 Resilient 共用）；429 下限 5s，
// 下限本身仍封顶 ceil——ceil<5s 的测试场景下最终钳到 ceil 的结果不变。
// isRateLimit 由调用方携带 ClassifyLLMError 结果，免二次全串分类。
func backoffDelay(attempt int, base, ceil time.Duration, isRateLimit bool) time.Duration {
	return backoffDelayJittered(attempt, base, ceil, isRateLimit, retryJitter)
}

// backoffDelayJittered 退避曲线唯一实现（llm#100 起抖动器可配）：与
// backoffDelay 同曲线同语义，抖动器由调用方注入——模型级 RateLimitRetry
// 装饰器用自己档位的抖动比例，Client/Resilient 继续用包级 retryJitter。
func backoffDelayJittered(attempt int, base, ceil time.Duration, isRateLimit bool, jitter mathx.Unstable) time.Duration {
	minDelay := base
	if isRateLimit {
		minDelay = min(5*time.Second, ceil)
	}
	delay := max(base*time.Duration(1<<uint(attempt-1)), minDelay)
	// jitter 按当次 delay 幅度（base/2 基准在高 attempt 档抖动占比趋近于零，
	// 打散效果失效——收口轮修）：单边向上 [delay, (1+ratio)·delay)，永不早于计算退避
	delay = min(jitter.UpwardDuration(delay), ceil)
	return delay
}

// GenerateJSON 生成并解析 JSON；解析失败把原始输出与错误回喂重试 1 次。
// out 必须是 *T。截断在重试核心内已转为错误返回，走到这里的解析失败只会是
// json.Unmarshal 错误——回喂提示只有"输出合法 JSON"一种。
// 刻意走 ExtractJSON+严格 Unmarshal 而非 jsonrepair.Unmarshal 宽容链：宽容修复
// 会改变输出内容，而本方法的失败路径要回喂重试，lastErr 须是纯解析错误。
// 宽容回收半损坏输出归 jsonrepair.Unmarshal（无重试回喂的场景用）。
func (c *Client) GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error {
	return c.generateJSONFitted(ctx, stage, msgs, out, c.retryPolicy())
}

// generateJSONFitted GenerateJSON 的 policy 参数版本——Client.GenerateJSON 与
// Resilient.tryOneProviderJSON 共用同一份「传输重试 + 回喂重试」骨架。此前
// Resilient 的 JSON 路径固定经 GenerateJSON 落到 Client 字段推导的 policy
// （buildClient 设 MaxRetries=1），同模型传输重试在 JSON 路径整体失效：一个
// 瞬态 500 就切模型，与 Generate 路径行为不对称（第六轮审计）。
// pol.attemptTimeout 经 generateRetry 参与每次物理尝试（Client 直用时来自
// AttemptTimeout 字段，llm#84）；Resilient 侧显式传 0——其 JSON 路径语义是
// "单次完整生成调用"共享一个外层 deadline（见 tryOneProviderJSON 的超时接线）。
func (c *Client) generateJSONFitted(ctx context.Context, stage string, msgs []*schema.Message, out any, pol retryPolicy) error {
	// 入口校验（reflect 改写前）：out 必须是非 nil 指针——原 json.Unmarshal 对
	// 误用返回 InvalidUnmarshalError，reflect 路径不校验会 panic
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("llm: %s GenerateJSON 的 out 必须是非 nil 指针（got %T）", stage, out)
	}
	ctx = obsx.WithStage(ctx, stage)
	// 每轮回喂自行 copy+fit（对齐 Generate 的入口纪律；本函数对 msgs 只读）
	var lastErr error
	lastRaw := ""
	for attempt := range 2 {
		callMsgs := copyMsgs(msgs)
		if attempt > 0 {
			hint := "你上一轮的输出无法解析为合法 JSON（错误：%v）。\n请重新输出，且只输出合法 JSON：不要解释、不要 markdown 代码围栏。"
			callMsgs = append(callMsgs,
				&schema.Message{Role: schema.Assistant, Content: lastRaw},
				schema.UserMessage(fmt.Sprintf(hint, lastErr)),
			)
		}
		if err := c.fitInput(callMsgs); err != nil {
			return fmt.Errorf("llm: %s %w", stage, err)
		}
		resp, attempts, err := c.generateRetry(ctx, stage, callMsgs, pol)
		if err != nil {
			return fmt.Errorf("llm: %s 调用失败（已重试 %d 次）: %w", stage, attempts-1, err)
		}
		raw := jsonrepair.ExtractJSON(resp.Content)
		// 解进临时零值、成功后整体赋回：encoding/json 失败/合并不清零已解码
		// 字段——重试共用同一 out 时，上次残留（如部分解码出的 Confidence）会
		// 泄入本次结果（v0.10.28 审计修复）
		fresh := reflect.New(reflect.TypeOf(out).Elem())
		if err := json.Unmarshal([]byte(raw), fresh.Interface()); err != nil {
			lastErr = err
			lastRaw = resp.Content
			continue
		}
		reflect.ValueOf(out).Elem().Set(fresh.Elem())
		return nil
	}
	return fmt.Errorf("llm: %s JSON 解析失败（已带错误重试 1 次）: %w", stage, lastErr)
}

// fitNoteMargin 截断注记余量（rune）：keep 在「预算差额」之外再扣一份，截完
// 的「正文+注记」总长才真正落在预算内——不扣则截完仍略超 limit（wrap/fit.go
// L2 同款收口，llm#188 第三项）。
const fitNoteMargin = 32

// fitNoteFloor 候选消息的 rune 数下限：低于此值的消息截断后「正文+注记」不缩
// 反涨（注记本身就 ~20 rune），参与裁剪即零进展死循环——不选（wrap L3 的
// 「置换后必须变小」守卫同款，第八轮审计）。
var fitNoteFloor = utf8.RuneCountInString("\n"+textutil.TruncNote("§", 0, textutil.FitInputNote)) + 4

// fitInput 发送前输入自守恒：总输入超 (窗口-输出预留)×2×0.9 时，截断最长的
// user 消息并留痕（兜底层，正常轮不到）。返回错误 = 全部可裁对象穷尽后仍超限
// （llm#188：不再静默放行——错误文案带 context too long marker，被
// ClassifyLLMError 判成确定性失败：单客户端立即失败不重试，降级链切换更大
// 窗口的备选模型；把失败从「provider 侧报错」提前到「本地可诊断失败」）。
func (c *Client) fitInput(msgs []*schema.Message) error {
	return c.fitInputFor(c.MaxOutputTokens, msgs)
}

// fitInputFor 指定输出预留额的输入自守恒（fitInput 的参数化形态）。截断提升轮
// 以 1.5 倍输出上限重算预算再裁剪（llm#83）——预留只认「本轮实际声明的输出
// 上限」，不认构造时的缺省值。
//
// 裁剪策略（llm#187/#188）：
//   - 计量口径 = MsgRuneCount（Content + ToolCalls + 三组多模态文本部件），
//     与 estimateMsgTokens 记账同字段面——tool-heavy 会话不再只按正文算总量
//     （此前 total 远小于真实发送量 → 判「未超限」直通 → provider 侧
//     context length exceeded，还被分类成确定性失败放弃重试）；
//   - 先截最长 user（与 wrap L2 同款：keep 扣注记余量，截完即入窗）；
//   - 裁后复查总量，仍未入窗回环迭代截最长可裁正文（User/Tool/Assistant，
//     对齐 wrap L3：错误/提醒结果保护不裁、system 提示词不裁）。ToolCalls
//     参数 JSON 不改写（协议配对完整性优先）——ToolCalls 占大头且无可裁正文
//     时收敛失败，返回可诊断错误而非静默直通。
func (c *Client) fitInputFor(outReserve int, msgs []*schema.Message) error {
	if c.ContextTokens <= 0 {
		return nil
	}
	// 预算公式单源 textutil.FitCharsLimit（第十轮收口：与 wrap.FitModel 双份
	// 手写漂移无编译期信号；CJK 感见 ×3/2 语义见彼处注释）
	limit := textutil.FitCharsLimit(c.ContextTokens, outReserve)
	if limit <= 0 {
		return nil
	}
	sizes := make([]int, len(msgs))
	total := 0
	for i, m := range msgs {
		sizes[i] = MsgRuneCount(m)
		total += sizes[i]
	}
	if total <= limit {
		return nil
	}
	// 已裁对象不再重选（裁过的消息只剩注记头，重复裁剪零进展）
	skip := make([]bool, len(msgs))
	// L2：截最长 user（keep 扣注记余量——此前不扣，截完仍略超限照发）
	if idx := longestIndex(msgs, sizes, skip, schema.User); idx >= 0 {
		keep := max(limit-(total-sizes[idx])-fitNoteMargin, 0)
		total -= trimMsgContent(msgs, sizes, idx, keep)
		if total <= limit {
			return nil
		}
		skip[idx] = true
	}
	// L3 回环：仍未入窗 → 迭代截最长可裁（User/Tool/Assistant）正文直至入窗
	// 或无可裁对象。此前只裁一条、裁完不复查：「其余消息之和本身已 ≥ limit」
	// 时 user 被削成注记、总量仍远超 limit 却已出函数（llm#188）。
	for total > limit {
		big := longestTrimable(msgs, sizes, skip)
		if big < 0 {
			break
		}
		keep := max(sizes[big]-(total-limit)-fitNoteMargin, 0)
		total -= trimMsgContent(msgs, sizes, big, keep)
		if total <= limit {
			return nil
		}
		skip[big] = true // 该消息已裁到注记头，本轮裁剪无进展空间
	}
	return fmt.Errorf("llm: 输入自守恒失败：裁剪全部可裁正文后仍超窗口预算（%d > %d rune，context too long）——请缩小输入或换更大窗口的模型",
		total, limit)
}

// longestIndex 指定角色中 rune 数最大的消息下标（-1 = 无）；已跳过（skip）的
// 不参与。
func longestIndex(msgs []*schema.Message, sizes []int, skip []bool, role schema.RoleType) int {
	idx := -1
	for i, m := range msgs {
		if m == nil || skip[i] || m.Role != role {
			continue
		}
		if idx < 0 || sizes[i] > sizes[idx] {
			idx = i
		}
	}
	return idx
}

// longestTrimable L3 候选：User/Tool/Assistant 中 rune 数最大且可裁（高于注记
// 下限、非保护结果）的消息下标（-1 = 无）。system 提示词不裁——指令被腰斩的
// 代价不可控，宁可交可诊断错误。
func longestTrimable(msgs []*schema.Message, sizes []int, skip []bool) int {
	big := -1
	for i, m := range msgs {
		if m == nil || skip[i] || sizes[i] <= fitNoteFloor {
			continue
		}
		switch m.Role {
		case schema.User, schema.Tool, schema.Assistant:
		default:
			continue
		}
		if isProtectedResult(m.Content) {
			continue // 错误/提醒结果保护（wrap L3 同款）：模型自纠依据不裁
		}
		if big < 0 || sizes[i] > sizes[big] {
			big = i
		}
	}
	return big
}

// trimMsgContent 就地截断 msgs[idx] 的 Content 为 keep rune + 留痕注记，返回
// 消息总 rune 的实际降幅（0 = 无进展——keep 不短于原正文或注记反涨；不动原
// 消息，调用方据此把该候选置 skip 收口，防死循环）。浅拷贝替换元素指针：
// fitInput 契约是只改副本、不污染调用方切片。
func trimMsgContent(msgs []*schema.Message, sizes []int, idx int, keep int) int {
	m := msgs[idx]
	cloned := *m
	cloned.Content = "\n" + textutil.TruncNote(m.Content, keep, textutil.FitInputNote)
	n := MsgRuneCount(&cloned)
	delta := sizes[idx] - n
	if delta <= 0 {
		return 0
	}
	msgs[idx] = &cloned
	sizes[idx] = n
	return delta
}

// isProtectedResult 错误/提醒结果保护（wrap/fit.go L3 同款语义，根包侧镜像）：
// 携带提醒信封（含 tool-error 标记）的结果是模型自纠与规则遵循的依据，
// 窗口裁剪不碰。两处实现须保持一致（wrap 不被根包导入，无法单源）。
func isProtectedResult(content string) bool {
	return strings.Contains(content, "<system-reminder>") || strings.Contains(content, "【tool-error】")
}
