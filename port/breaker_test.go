package port

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oekit/concurrency/breaker"
)

// failN 对 "m" 桶连续报 n 次失败（每次先 Allow 领代）。closed 态下速率型触发
// 要 6 个样本（上游 protection=5）才可能开闸，前 n 次 Allow 必然放行。
func failN(t *testing.T, b *Breakers, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		gen, ok := b.Allow(ctx, "m")
		if !ok {
			t.Fatalf("closed 态第 %d 次 Allow 应放行", i+1)
		}
		b.Failure(ctx, "m", gen)
	}
}

// TestNewBreakersAppliesPackageDefaults 回归（oagentkit/llm#165）：本包导出
// DefaultBreakerTrip=3 / DefaultBreakerCooldown=5min 并声明「是本包对外承诺的
// 缺省」，但 NewBreakers 此前零 option 直取上游缺省——consecutive=0（计数型
// 触发整个关闭）+ 冷却回落 probeTimeout 1min，port.Chain 的熔断缺省既不按
// 3 次连败开闸、冷却也短 5 倍。
//
// 假时钟 + 关掉 1s 强制探活（DefaultForcePass 的观察哨流量会打断「开闸期内
// 仍拒绝」的断言），让冷却断言不依赖真实时间流逝。
func TestNewBreakersAppliesPackageDefaults(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	clock := func() time.Time { return now }
	base := func() []breaker.BreakerOption {
		return []breaker.BreakerOption{
			breaker.WithBreakerClock(clock),
			breaker.WithForcePass(24 * time.Hour),
		}
	}

	// 缺省：3 次连败开闸，冷却 DefaultBreakerCooldown=5min。
	b := NewBreakers(base()...)
	failN(t, b, 3)
	if st := b.Get("m").State(); st != breaker.StateOpen {
		t.Fatalf("缺省 trip=3 应在 3 次连败后开闸（llm#165），state=%v", st)
	}
	now = now.Add(4 * time.Minute)
	if _, ok := b.Allow(ctx, "m"); ok {
		t.Fatal("开闸 4min 应仍拒绝——缺省冷却 5min，不是上游回落的 probeTimeout 1min（llm#165）")
	}
	now = now.Add(time.Minute + time.Second)
	if _, ok := b.Allow(ctx, "m"); !ok {
		t.Fatal("冷却满 5min 应放行半开探测")
	}
}

// TestNewBreakersExplicitOptsOverrideDefaults钉「未传 vs 显式传」语义
// （oagentkit/llm#165 建议修复的关键约束：不能拿 0 表达「用缺省」）：调用方
// 传的 opts 在缺省铺底之后应用、逐项覆盖。
func TestNewBreakersExplicitOptsOverrideDefaults(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	clock := func() time.Time { return now }

	// 显式 trip=5：3 次连败不触发缺省的 3。
	b5 := NewBreakers(
		breaker.WithConsecutiveFailures(5),
		breaker.WithBreakerClock(clock),
		breaker.WithForcePass(24*time.Hour),
	)
	failN(t, b5, 3)
	if _, ok := b5.Allow(ctx, "m"); !ok {
		t.Fatal("显式 WithConsecutiveFailures(5) 应覆盖缺省 3：3 次连败后仍放行")
	}

	// 显式 consecutive=0（上游语义 = 关闭计数型）同样覆盖缺省，不被铺底改回 3。
	b0 := NewBreakers(
		breaker.WithConsecutiveFailures(0),
		breaker.WithBreakerClock(clock),
		breaker.WithForcePass(24*time.Hour),
	)
	failN(t, b0, 3)
	if _, ok := b0.Allow(ctx, "m"); !ok {
		t.Fatal("显式 WithConsecutiveFailures(0) 应覆盖缺省 3（关闭计数型）")
	}
}

// TestChainDefaultBreakerTripsAfterThreeFailures 端到端（oagentkit/llm#165）：
// NewChain 缺省装配的熔断须按 DefaultBreakerTrip=3 连败开闸——此前缺省是上游
// consecutive=0，连败 3 次后第 4 次照样放行打真实调用（冷却也只有 1min）。
func TestChainDefaultBreakerTripsAfterThreeFailures(t *testing.T) {
	f := &fakeCompleter{err: errors.New("上游 500")}
	// attempts 缺省 1：一次 Complete = 一次物理调用 = 一次 Failure 回报。
	chain := NewChain(WithCompleter("m", f))
	ctx := context.Background()

	for i := range 3 {
		if _, err := chain.Complete(ctx, Request{Prompt: "hi"}); err == nil {
			t.Fatalf("第 %d 次应失败上抛", i+1)
		}
	}
	if _, err := chain.Complete(ctx, Request{Prompt: "hi"}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("连败 3 次后第 4 次应熔断拒绝（llm#165），实际 %v", err)
	}
	if f.calls != 3 {
		t.Fatalf("熔断后不应再打真实调用，物理调用次数 = %d（应为 3）", f.calls)
	}
}
