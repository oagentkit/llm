package llm

import (
	"context"
	"sync"
	"time"

	"github.com/oekit/concurrency/breaker"
)

// 熔断默认值。放在 llm 而非直接引用上游常量：上游 breaker 未导出默认值（它的缺省是
// 「自适应丢弃」，没有 trip/cooldown 概念），而这里的三个值是本包对外承诺的缺省，
// 属于 ResilientConfig 的 API 契约，不随上游变化。
const (
	// defaultBreakerTrip 模型熔断阈值（连续失败次数）。
	defaultBreakerTrip = 3
	// defaultBreakerCooldown 模型熔断冷却期。
	defaultBreakerCooldown = 5 * time.Minute
	// defaultBreakerProbeTimeout 半开探测的单次时限（用户显式置零时的兜底）。
	defaultBreakerProbeTimeout = time.Minute
)

// breakers 是按模型名分桶的熔断器集合。
//
// 状态机（显式 closed/open/half-open、代绑定、冷却期、探测时限）全部委托给
// github.com/oekit/concurrency/breaker 的 GenBreaker，本文件只补一件事：
// 计数型触发的「准入否决」。
//
// ## 为什么需要自己数
//
// 上游 WithConsecutiveFailures 确实会把状态切到 open，但放行路径只读速率型的
// dropRatio、不看状态：纯失败场景下 weightedAccepts=0，dropRatio=(total-5)/(total+1)，
// 在样本累积过 protection 之前恒 ≤ 0，于是「open 态也全量放行」。实测阈值设 3 、
// 真正停下来要到第 12 次。已报上游 https://github.com/oekit/concurrency/issues/25 。
//
// 在上游修好之前，这里用一个计数器补上准入侧的否决：计数器只做「连挂 N 次 → 到冷却期
// 为止拒绝」，状态迁移、窗口统计、探测配对仍归上游。上游修好后本段可整体删除。
//
// ## 为什么按模型名分桶
//
// 降级链要按模型分桶统计——一个 breaker 覆盖多个模型名会把互相独立的故障率混成一路，
// 一个模型的连挂会误伤另一个。
type breakers struct {
	mu     sync.Mutex
	byName map[string]*modelBreaker
	opts   []breaker.BreakerOption

	trip       int
	cooldown   time.Duration
	clock      func() time.Time
	newBreaker func(name string) breaker.GenBreaker
}

// modelBreaker 是单个模型名的熔断状态。
type modelBreaker struct {
	gb breaker.GenBreaker

	mu          sync.Mutex
	consecFails int       // 连续失败数；成功/abandon 清零
	vetoUntil   time.Time // 准入否决到期时刻（= 冷却期满）
}

func newBreakers(trip int, cooldown time.Duration, opts ...breaker.BreakerOption) *breakers {
	if trip <= 0 {
		trip = defaultBreakerTrip
	}
	if cooldown <= 0 {
		cooldown = defaultBreakerCooldown
	}
	return &breakers{
		byName: make(map[string]*modelBreaker),
		opts:   opts,
		trip:   trip,
		// cooldown 与探测时限是两件事：冷却是业务决策（多久敢再试），
		// 探测时限是调用在途上界（单次尝试多久没回来算失联）。
		cooldown: cooldown,
		clock:    time.Now,
		newBreaker: func(name string) breaker.GenBreaker {
			// 上游 *circuitBreaker 同时实现 Breaker 与 GenBreaker，取扩展接口。
			gb, _ := breaker.NewBreaker(name, opts...).(breaker.GenBreaker)
			return gb
		},
	}
}

// get 取（或首次创建）某模型名的熔断器。
func (b *breakers) get(name string) *modelBreaker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if mb, ok := b.byName[name]; ok {
		return mb
	}
	mb := &modelBreaker{gb: b.newBreaker(name)}
	b.byName[name] = mb
	return mb
}

// allow 判定放行并返回本次放行的代。ok=false 表示熔断拒绝。
//
// 顺序：先查本地准入否决（连续失败计数），再问上游。ctx 已取消时不放行也不消耗统计
// —— 与回报侧的 ctx 门对称：用户主动中止不该把模型推向熔断。
func (b *breakers) allow(ctx context.Context, name string) (gen uint64, ok bool) {
	if err := ctx.Err(); err != nil {
		return 0, false
	}
	mb := b.get(name)
	mb.mu.Lock()
	vetoed := b.clock().Before(mb.vetoUntil)
	mb.mu.Unlock()
	if vetoed {
		return 0, false
	}
	_, gen, err := mb.gb.AllowGen(ctx)
	if err != nil {
		return 0, false
	}
	return gen, true
}

// success 回报成功：陈旧代作废，并清零连续失败计数。
func (b *breakers) success(name string, gen uint64) {
	mb := b.get(name)
	mb.gb.SuccessGen(gen)
	mb.mu.Lock()
	mb.consecFails = 0
	mb.mu.Unlock()
}

// abandon 放弃一次放行：探针失联（或用户主动中止）的确定性回收——腾出探测位，
// 不计失败统计。走 Failure 会让「连续 N 次用户取消」把模型误熔断。
func (b *breakers) abandon(name string, gen uint64) {
	b.get(name).gb.AbandonGen(gen)
}

// failure 回报失败。陈旧代回报上游会整体作废；本地计数同样只对有效放行累加。
func (b *breakers) failure(ctx context.Context, name string, gen uint64) {
	mb := b.get(name)
	mb.gb.FailureGen(ctx, gen)

	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.consecFails++
	if mb.consecFails >= b.trip {
		mb.vetoUntil = b.clock().Add(b.cooldown)
		mb.consecFails = 0
	}
}
