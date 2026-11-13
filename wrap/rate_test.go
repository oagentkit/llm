package wrap

import (
	"context"
	"math"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/oekit/concurrency/ratelimit"
)

func TestRateModelNilPassthrough(t *testing.T) {
	inner := &fakeChatModel{}
	if got := NewRateModel(inner, nil); got != model.BaseChatModel(inner) {
		t.Fatal("nil limiter 应返回原对象（零开销直通）")
	}
}

func TestRateModelGenerateForwards(t *testing.T) {
	inner := &fakeChatModel{}
	// Inf 等价形态：巨大 rps（x/time rate.Inf = math.Inf(1)，TokenBucket 内部
	// 即 x/time Limiter，语义一致）
	rm := NewRateModel(inner, ratelimit.NewTokenBucketFloat(math.Inf(1), 1)).(*RateModel)
	out, err := rm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil || out == nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestRateModelRespectsCancel(t *testing.T) {
	inner := &fakeChatModel{}
	// 近零配额限速器 + 已取消 ctx：Wait 立即返回错误（取消态走 ctx.Err()），
	// 请求不出网。burst=1 是新契约下的最小合法桶——concurrency #291 起
	// burst<=0 在构造期 panic（x/time/rate 语义下 burst<1 即恒拒，
	// 没有合法使用场景），不再有「构造成功但 Wait 报 ErrBurstTooSmall」的形态
	rm := NewRateModel(inner, ratelimit.NewTokenBucketFloat(0.0001, 1)).(*RateModel)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rm.Generate(ctx, []*schema.Message{bigTool(schema.User, 10)}); err == nil {
		t.Fatal("取消的 ctx 应立即返回错误")
	}
	inner.mu.Lock()
	called := inner.lastIn != nil
	inner.mu.Unlock()
	if called {
		t.Fatal("限速等待失败不应触达内层模型")
	}
}

func TestRateModelWithToolsKeepsLimiter(t *testing.T) {
	inner := &fakeChatModel{}
	lim := ratelimit.NewTokenBucketFloat(math.Inf(1), 1)
	rm := NewRateModel(inner, lim).(*RateModel)
	bound, err := rm.WithTools([]*schema.ToolInfo{})
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}
	bm, ok := bound.(*RateModel)
	if !ok || bm.Limiter != lim {
		t.Fatal("绑定产物应保持限速器")
	}
}
