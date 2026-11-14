// usage.go — LLM 用量采集（eino callbacks）——**全仓唯一的 callbacks 侧记账
// 出口**：拿得到 Cached/Reasoning tokens、FinishReason、Duration、Iteration。
// 归因经 Labels（map）透传，领域键由调用方决定。
// （v0.10.12 前 obsx.Options.OnUsage 是并行的五数字出口，防重护栏跨包放在
// obsx——现护栏归位于本包，obsx 回归纯 trace，ReAct/RawModel 旁路记账统一
// 走 NewUsageHandler 注入 callbacks。）
package llm

import (
	"context"
	"github.com/cloudwego/eino/schema"
	"github.com/oekit/concurrency/async"
	"maps"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"

	"github.com/oagentkit/llm/obsx"
)

// UsageRecord 单次 LLM 调用的用量记录。
type UsageRecord struct {
	Model            string
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
	ReasoningTokens  int
	TotalTokens      int
	// Partial 失败面标记（llm#191）：true = 该记录来自失败路径的 prompt 侧估算
	// （请求已发出、provider 大概率已受理计费，但失败拿不到 usage——
	// completion 记 0）。聚合口径：PromptTokens/TotalTokens 照常入账（预算与
	// 成本对失败面不再系统性偏低），判读 completion 侧指标时应排除 Partial 记录。
	Partial      bool
	DurationMS   int64
	FinishReason string
	Iteration    int64             // 本作用域第几次 LLM 调用（1 起；无计数器为 0）
	Labels       map[string]string // 归因标签（session/case/step/agent 等，调用方自定）
}

// Sink 用量出口。
type Sink func(UsageRecord)

type labelsKey struct{}

// WithUsageLabels ctx 注入归因标签；不注入或无标签时 UsageRecord.Labels 为 nil。
//
// ⚠️ 注入即拷贝（快照语义，oagentkit/llm#68）：采集器从 ctx 读的是**注入时刻
// 的快照**——晚定型归因（如选链完成后才回填的模型名）必须**二次注入整包标签**
// （新 ctx 再传给后续调用）；只改原 map 不重注，后续记录静默退化成组件类型
// 口径（Model 填 eino RunInfo.Type），定价恒算不出价。「何时必须重注」没有
// 类型面约束，靠本注释纪律——调用方在自己的装配层用注释钉住注入点清单。
func WithUsageLabels(ctx context.Context, labels map[string]string) context.Context {
	if len(labels) == 0 {
		return ctx
	}
	// 拷贝防调用方后续修改。
	cp := make(map[string]string, len(labels))
	maps.Copy(cp, labels)
	return context.WithValue(ctx, labelsKey{}, cp)
}

func labelsFrom(ctx context.Context) map[string]string {
	m, _ := ctx.Value(labelsKey{}).(map[string]string)
	return m
}

// UsageCallCounter 单作用域 LLM 调用轮次计数（原子递增）。
type UsageCallCounter struct{ n atomic.Int64 }

type counterKey struct{}

// WithCallCounter ctx 挂轮次计数器（每个 agent 运行作用域一个）。
func WithCallCounter(ctx context.Context) context.Context {
	return context.WithValue(ctx, counterKey{}, &UsageCallCounter{})
}

type startKey struct{}

// clientAccountingCtxKey 标记「本次模型调用的 token 记账由 Client 侧负责」
// （Client 配置了任一遥测出口 OnUsage/OnUsageDetailed 时 generateRetry 打点）：
// NewUsageHandler 检测到标记跳过发射——同一物理调用的 Generate 路径与
// callbacks 路径只记一次账。
type clientAccountingCtxKey struct{}

func withClientAccounting(ctx context.Context) context.Context {
	return context.WithValue(ctx, clientAccountingCtxKey{}, true)
}

func clientAccounted(ctx context.Context) bool {
	v, _ := ctx.Value(clientAccountingCtxKey{}).(bool)
	return v
}

// NewUsageHandler 构建 eino callbacks.Handler（非流式 OnEnd + 流式
// OnEndWithStreamOutput——流式分支在后台 goroutine 排空副本流后回调，
// ⚠ onRecord 可能被多 goroutine 并发调用，须并发安全（第十轮审计钉约）。
// OnEndWithStreamOutput——第八轮审计补齐：eino-ext 流式只触发后者，仅注册
// OnEnd 的 handler 在开流（agentrun v0.10.32 起默认）下一条记录都不产生，
// 静默失明。流式分支消费框架给的副本流取末个非 nil usage，末帧即汇总帧）。
// 无 TokenUsage、无 RunInfo 的调用静默跳过；Labels 缺省不阻断采集。
// 防重：Client 自身配置了任一遥测出口（OnUsage / OnUsageDetailed，llm#132 起
// 两者同口径参与防重）时（Client 记账标记已在 ctx），本 handler 对同一次物理
// 调用自动跳过——两侧不会双倍记账。⚠ Budget **不在防重面**（client.go 同款
// 口径）：Budget 是预算扣减不是遥测出口、不打标记，OnUsage + Budget + 本
// handler 三件套同时装配会把同一笔用量计两处（handler 一笔 + Budget 扣一笔）
// ——账单口径只看遥测出口则无碍，但两处都消费即翻倍。
// ⚠ agentrun 场景：Config.OnUsage 与本 handler 记同一笔——**择一装配**
// （两者没有共享防重标记，同时挂会双计）。
// DurationMS 以最近一次 OnStart（即本次调用）起点起算（llm#115）：handler
// 挂在根/全局时，agent 循环每轮调用各自计时，不继承外层作用域起点。
func NewUsageHandler(onRecord Sink) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			// 起点=最近一次调用的起点（llm#115）：handler 挂在根/全局时，agent
			// ReAct 循环第 N 次模型调用的 OnStart 必须刷新起点，否则 DurationMS
			// 从根 OnStart 起算、随轮次单调虚涨（token 面板耗时观测失真）。此前
			// 的「已有起点不覆盖」防重是给装饰器链同一次调用双触发 OnStart 设计
			// 的——该场景两次几乎同时，取后者（更贴近模型实际执行）无碍；与
			// 跨调用嵌套不可同日而语，统一改为每次 OnStart 都起新起点。
			return context.WithValue(ctx, startKey{}, time.Now())
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if rec, ok := usageRecordFrom(ctx, info, output); ok && onRecord != nil {
				onRecord(rec)
			}
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if onRecord == nil || clientAccounted(ctx) || info == nil || info.Type == "" {
				// 不消费也必须关副本（eino 契约：handler 副本不关整条管道泄漏）
				output.Close()
				return ctx
			}
			// **异步消费**（第九轮审计 C 级）：eino 的 handler 分发是同步的、
			// Stream() 在 OnEndWithStreamOutput 返回后才把流交给调用方——
			// 此前同步排空副本流会把真流式阻塞成「全量生成完才返回」（首分片
			// 延迟=全程生成时间，v0.10.32 起默认开流的 text_delta 全部积压）。
			// ctx/info 为只读值，goroutine 内读取安全；Iteration 为 atomic。
			async.GoSafe(func() {
				defer output.Close()
				usage, _, finish := obsx.DrainStreamUsage(output)
				if usage == nil {
					return
				}
				// finish reason 从流末帧提取（llm#70）：length 截断在流式
				// 观测面此前恒不可见（两路不对称），截断提升重试的信号缺失
				onRecord(recordFromUsage(ctx, info, usage, finish))
			})
			return ctx
		}).
		Build()
}

// recordFromUsage 已知 usage 的记录构造（非流式/流式两路共用单源）。
func recordFromUsage(ctx context.Context, info *callbacks.RunInfo, usage *model.TokenUsage, finish string) UsageRecord {
	rec := UsageRecord{
		Model:            info.Type,
		PromptTokens:     usage.PromptTokens,
		CachedTokens:     usage.PromptTokenDetails.CachedTokens,
		CompletionTokens: usage.CompletionTokens,
		ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      usage.TotalTokens,
		Labels:           labelsFrom(ctx),
	}
	// provider 只回分项、TotalTokens 缺席（DeepSeek/MiMo 等兼容端点常见）时
	// 兜底重算 prompt+completion（llm#195）：与 wrap/continue.go
	// mergeContinueMeta 的「total 重算保持自洽」同一口径——按 TotalTokens
	// 聚合的 token 看板不再因 total 缺席而恒 0（分项非 0 的静默失真）。
	if rec.TotalTokens == 0 {
		rec.TotalTokens = rec.PromptTokens + rec.CompletionTokens
	}
	if start, ok := ctx.Value(startKey{}).(time.Time); ok && !start.IsZero() {
		rec.DurationMS = time.Since(start).Milliseconds()
	}
	if c, ok := ctx.Value(counterKey{}).(*UsageCallCounter); ok {
		rec.Iteration = c.n.Add(1)
	}
	rec.FinishReason = finish
	return rec
}

// usageRecordFrom 从 OnEnd 上下文与回调输出提取用量记录。
// ok=false 表示应静默跳过（Client 已记账 / 无 RunInfo / 无真实用量）。
func usageRecordFrom(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) (UsageRecord, bool) {
	// Client 侧已记账（任一遥测出口 OnUsage/OnUsageDetailed 配置时
	// generateRetry 打点，llm#132）→ 跳过。
	if clientAccounted(ctx) {
		return UsageRecord{}, false
	}
	// 只认带 RunInfo 的 OnEnd，否则 token 双计、iteration 虚增。
	if info == nil || info.Type == "" {
		return UsageRecord{}, false
	}
	out := model.ConvCallbackOutput(output)
	if out == nil {
		return UsageRecord{}, false
	}
	// 真实用量提取（含 compose 只透传 Message 的回退）单源 obsx.TokenUsageOf。
	usage := obsx.TokenUsageOf(out)
	if usage == nil {
		return UsageRecord{}, false
	}
	finish := ""
	if out.Message != nil && out.Message.ResponseMeta != nil {
		finish = out.Message.ResponseMeta.FinishReason
	}
	return recordFromUsage(ctx, info, usage, finish), true
}
