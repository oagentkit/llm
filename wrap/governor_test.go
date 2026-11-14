package wrap

// #198 下沉测试：在飞闸装饰器的转发、在飞封顶、流式名额覆盖、
// WithTools 派生共享状态（issue #198 核心契约）与 Checker 透传。

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oekit/concurrency/syncx"
)

// streamFake 可脚本化的流式桩：正常出 chunks 个分片后收口；failAt>0 时在
// failAt 个分片后注入错误帧。WithTools 返回自身（派生共享同一内层）。
type streamFake struct {
	mu     sync.Mutex
	calls  int
	chunks int
	failAt int
}

func (f *streamFake) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return bigTool(schema.Assistant, 5), nil
}

func (f *streamFake) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	out, w := schema.Pipe[*schema.Message](1)
	go func() {
		for i := 0; i < f.chunks; i++ {
			if w.Send(&schema.Message{Role: schema.Assistant, Content: "c"}, nil) {
				return
			}
			if f.failAt > 0 && i+1 == f.failAt {
				w.Send(nil, errors.New("streamfake: 注入错误"))
				w.Close()
				return
			}
		}
		w.Close()
	}()
	return out, nil
}

func (f *streamFake) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return f, nil }

func (f *streamFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// checkerInner 可开关的 Checker 桩：验证 IsCallbacksEnabled 透传内层判定。
type checkerInner struct {
	*fakeChatModel
	enabled bool
}

func (c *checkerInner) IsCallbacksEnabled() bool { return c.enabled }

// drainStream 读完流直至 EOF（测试助手）。
func drainStream(t *testing.T, sr *schema.StreamReader[*schema.Message]) {
	t.Helper()
	for {
		if _, err := sr.Recv(); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("收流: %v", err)
			}
			return
		}
	}
}

func TestGovernorModelNilGatePassthrough(t *testing.T) {
	inner := &fakeChatModel{}
	gm := NewGovernorModel(inner, "m", nil)
	if _, err := gm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err != nil {
		t.Fatalf("nil Gate 应直通: %v", err)
	}
	inner.mu.Lock()
	called := inner.lastIn != nil
	inner.mu.Unlock()
	if !called {
		t.Fatal("直通应触达内层模型")
	}
}

func TestGovernorModelGenerateForwards(t *testing.T) {
	inner := &fakeChatModel{}
	gm := NewGovernorModel(inner, "m", syncx.NewKeyedGate(2))
	in := []*schema.Message{bigTool(schema.User, 10)}
	out, err := gm.Generate(context.Background(), in)
	if err != nil || out == nil {
		t.Fatalf("Generate: %v", err)
	}
	inner.mu.Lock()
	same := len(inner.lastIn) == len(in) && inner.lastIn[0] == in[0]
	inner.mu.Unlock()
	if !same {
		t.Fatal("输入应原样转发内层（在飞闸不改消息）")
	}
}

// TestGovernorModelCapsInFlight 名额封顶：cap=1 的闸，名额被占时后续调用排队
// （ctx 取消即中止，错误带 key 留痕），内层不触达；名额归还后放行。
func TestGovernorModelCapsInFlight(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{bigTool(schema.Assistant, 5)}}
	gate := syncx.NewKeyedGate(1)
	gm := NewGovernorModel(inner, "m1", gate)

	hold, err := gate.Acquire(context.Background(), "m1") // 手动占满唯一名额
	if err != nil {
		t.Fatalf("占名额: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = gm.Generate(ctx, []*schema.Message{bigTool(schema.User, 10)})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "在飞排队被中止") {
		t.Fatalf("名额占满时应排队并被取消中止（带 key 留痕），got %v", err)
	}
	if inner.callCount() != 0 {
		t.Fatal("排队中止不应触达内层模型")
	}
	hold()
	if _, err := gm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err != nil {
		t.Fatalf("名额归还后应放行: %v", err)
	}
	if inner.callCount() != 1 {
		t.Fatalf("归还后应触达内层一次，got %d", inner.callCount())
	}
}

// TestGovernorModelWithToolsSharesGate issue #198 核心契约：WithTools 派生
// 共享状态——派生实例与原实例共享同一在飞闸（同 key 同名额池），「派生即
// 新状态」= 闸门失效的路径必须不存在。
func TestGovernorModelWithToolsSharesGate(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{bigTool(schema.Assistant, 5)}}
	gate := syncx.NewKeyedGate(1)
	base := NewGovernorModel(inner, "m1", gate)

	d1, err := base.WithTools([]*schema.ToolInfo{})
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}
	dm := d1.(*GovernorModel)
	if dm.Gate != gate || dm.Name != base.Name {
		t.Fatal("派生实例必须原样持有同一 Gate 指针与 key")
	}

	hold, err := gate.Acquire(context.Background(), "m1")
	if err != nil {
		t.Fatalf("占名额: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// 占满后 base 与派生实例同样被堵：证明两者消费同一名额池
	if _, err := dm.Generate(ctx, []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("派生实例应与原实例共享闸门（同 key 被堵），got %v", err)
	}
	if _, err := base.Generate(ctx, []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("原实例同 key 应被堵，got %v", err)
	}
	if inner.callCount() != 0 {
		t.Fatal("共享闸门被堵时不应触达内层")
	}
	hold()
	if _, err := dm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err != nil {
		t.Fatalf("派生实例放行后应触达内层: %v", err)
	}
	if inner.callCount() != 1 {
		t.Fatalf("派生实例放行后应恰好一次调用，got %d", inner.callCount())
	}
}

// TestGovernorModelStreamHoldsSlotUntilConsumed 流式名额覆盖：在飞名额覆盖整段
// 流消费——流未读完，名额不归还（第二路调用排队）；读完收口即归还。
func TestGovernorModelStreamHoldsSlotUntilConsumed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := &streamFake{chunks: 2}
		gate := syncx.NewKeyedGate(1)
		gm := NewGovernorModel(inner, "m1", gate)

		sr, err := gm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		// 名额在飞：同 key 再取名额应被堵（名额满 + 已取消 ctx，确定性失败）
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := gate.Acquire(ctx, "m1"); err == nil {
			t.Fatal("流未读完不应归还名额")
		}
		// 第二路调用排队等待名额
		done := make(chan error, 1)
		go func() {
			_, err := gm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
			done <- err
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("流未读完时第二路调用不应完成")
		default:
		}
		// 读完流（EOF 收口），名额归还
		drainStream(t, sr)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatalf("名额归还后第二路调用应完成: %v", err)
		}
		if inner.callCount() != 2 {
			t.Fatalf("应恰好两次调用（1 流式 + 1 排队后放行），got %d", inner.callCount())
		}
	})
}

func TestGovernorModelStreamForwardsChunksAndError(t *testing.T) {
	t.Run("正常分片转发", func(t *testing.T) {
		inner := &streamFake{chunks: 3}
		gm := NewGovernorModel(inner, "m", syncx.NewKeyedGate(2))
		sr, err := gm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		n := 0
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("收流: %v", err)
				}
				break
			}
			n++
			if chunk.Content != "c" {
				t.Fatalf("分片应原样转发, got %q", chunk.Content)
			}
		}
		if n != 3 {
			t.Fatalf("应转发 3 个分片, got %d", n)
		}
	})
	t.Run("错误帧透传", func(t *testing.T) {
		inner := &streamFake{chunks: 1, failAt: 1}
		gm := NewGovernorModel(inner, "m", syncx.NewKeyedGate(2))
		sr, err := gm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if _, err := sr.Recv(); err != nil {
			t.Fatalf("首分片应到达: %v", err)
		}
		if _, err := sr.Recv(); err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("错误帧应透传消费方, got %v", err)
		}
		sr.Close() // eino 契约：出错退出也须恰好 Close 一次
	})
}

func TestGovernorModelCheckerPassthrough(t *testing.T) {
	inner := &checkerInner{fakeChatModel: &fakeChatModel{}, enabled: true}
	gm := NewGovernorModel(inner, "m", syncx.NewKeyedGate(1))
	if !gm.IsCallbacksEnabled() {
		t.Fatal("应透传内层 enabled=true")
	}
	inner.enabled = false
	if gm.IsCallbacksEnabled() {
		t.Fatal("应透传内层 enabled=false")
	}
	// 内层不实现 Checker：判定为 false（与既有装饰器口径一致——不透传时
	// compose 会在节点外再注入一轮回调，usage 双计）
	plain := NewGovernorModel(&fakeChatModel{}, "m", syncx.NewKeyedGate(1))
	if plain.IsCallbacksEnabled() {
		t.Fatal("内层不实现 Checker 时应为 false")
	}
}
