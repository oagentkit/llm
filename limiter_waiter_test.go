package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/llmtest"
	"github.com/oekit/concurrency/ratelimit"
)

// 钉子：oekit ratelimit 标准实现自然满足 Waiter（Go 接口隐式满足，宿主零适配器）。
var _ Waiter = ratelimit.NewTokenBucketFloat(1, 1)

// onlyWaiter 只实现 Wait 的宿主自带限速器——消费面收窄成最小面后，这种形态
// 可以直接塞进 Client.Limiter / Resilient.Limiter（四方法面时代会编译拒绝，
// 迫使宿主为三个永远不被调用的方法写空实现，其中 Reserve 的语义映射没有合理
// 答案：接口无拒绝语义，x/time Reserve 有 ok=false 路径，对不齐）。
type onlyWaiter struct{ waits int }

func (w *onlyWaiter) Wait(_ context.Context) error { w.waits++; return nil }

// rejectWaiter Wait 返回 err 的限速器——错误必须原样上抛（ctx 取消等）。
type rejectWaiter struct{ err error }

func (w *rejectWaiter) Wait(_ context.Context) error { return w.err }

// TestClientLimiterMinimalWaiterConsumed 最小面限速器在真实物理调用路径
//（oneAttempt）里被消费：Wait 恰好一次。
func TestClientLimiterMinimalWaiterConsumed(t *testing.T) {
	w := &onlyWaiter{}
	c := NewClient(&llmtest.Model{Script: []llmtest.Resp{{Content: "ok"}}}, "test", nil)
	c.Limiter = w
	if _, err := c.oneAttempt(context.Background(), "stage", []*schema.Message{{Role: schema.User, Content: "hi"}}, 0, nil); err != nil {
		t.Fatalf("oneAttempt: %v", err)
	}
	if w.waits != 1 {
		t.Fatalf("Wait 调用次数 = %d, want 1", w.waits)
	}
}

// TestClientLimiterWaitErrorPropagates 限速器错误原样上抛（包一层 stage 上下文），
// 不吞不重试——ctx 取消类错误进重试判断只会白等。
func TestClientLimiterWaitErrorPropagates(t *testing.T) {
	sentinel := errors.New("ctx done")
	c := NewClient(&llmtest.Model{}, "test", nil)
	c.Limiter = &rejectWaiter{err: sentinel}
	_, err := c.oneAttempt(context.Background(), "stage", []*schema.Message{{Role: schema.User, Content: "hi"}}, 0, nil)
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrap %v", err, sentinel)
	}
}

// TestResilientLimiterAcceptsMinimalWaiter 字段类型为 Waiter：仅实现 Wait 的
// 实现可赋值（编译面钉子）。
func TestResilientLimiterAcceptsMinimalWaiter(t *testing.T) {
	r := &Resilient{}
	r.Limiter = &onlyWaiter{}
	if r.Limiter == nil {
		t.Fatal("Limiter 赋值丢失")
	}
}
