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
)

// breakers 是按模型名分桶的熔断器集合。
//
// 状态机全部委托 github.com/oekit/concurrency/breaker 的 GenBreaker：显式
// closed/open/half-open 三态、放行与回报的代绑定（陈旧代整体作废）、冷却期与探测
// 时限可独立配置、计数型触发与速率型并联——任一命中即切断。
//
// 本层不做任何熔断语义兜底。v0.13.0 之前上游计数型只切状态、切不了放行决策
// （见 oekit/concurrency#25），当时本文件带一个本地准入否决计数器；v0.13.0 起
// 上游已修（同一探针 5/5 轮均在第 N+1 次放行返回 ErrCircuitOpen），那段兜底已删除。
//
// 为什么按模型名分桶：降级链要按模型独立统计——一个 breaker 覆盖多个模型名会把互相
// 独立的故障率混成一路，一个模型的连挂会误伤另一个。
type breakers struct {
	mu     sync.Mutex
	byName map[string]breaker.GenBreaker
	opts   []breaker.BreakerOption
}

func newBreakers(opts ...breaker.BreakerOption) *breakers {
	return &breakers{
		byName: make(map[string]breaker.GenBreaker),
		opts:   opts,
	}
}

// get 取（或首次创建）某模型名的熔断器。
func (b *breakers) get(name string) breaker.GenBreaker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gb, ok := b.byName[name]; ok {
		return gb
	}
	// 上游 *circuitBreaker 同时实现 Breaker 与 GenBreaker，这里取扩展接口。
	gb, _ := breaker.NewBreaker(name, b.opts...).(breaker.GenBreaker)
	b.byName[name] = gb
	return gb
}

// allow 判定放行并返回本次放行的代。ok=false 表示熔断拒绝。
//
// ctx 已取消时不放行也不消耗统计——与回报侧的 ctx 门对称：用户主动中止不该把模型
// 推向熔断（TestResilientUserCancelDoesNotTripBreaker 钉的就是这条）。
func (b *breakers) allow(ctx context.Context, name string) (gen uint64, ok bool) {
	_, gen, err := b.get(name).AllowGen(ctx)
	if err != nil {
		return 0, false
	}
	return gen, true
}

// success 回报成功：陈旧代作废。
func (b *breakers) success(name string, gen uint64) { b.get(name).SuccessGen(gen) }

// abandon 放弃一次放行：探针失联（或用户主动中止）的确定性回收——腾出探测位，
// 不计失败统计。走 Failure 会让「连续 N 次用户取消」把模型误熔断。
func (b *breakers) abandon(name string, gen uint64) { b.get(name).AbandonGen(gen) }

// failure 回报失败。ctx 已取消时不消耗统计。
func (b *breakers) failure(ctx context.Context, name string, gen uint64) {
	b.get(name).FailureGen(ctx, gen)
}
