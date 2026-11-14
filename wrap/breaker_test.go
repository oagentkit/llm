package wrap

// #198 下沉测试：熔断装饰器的转发、连续失败跳闸、冷却半开（synctest 假时钟）、
// 用户取消 Abandon 不计失败、WithTools 派生共享状态（issue #198 核心契约）、
// 流式结果回填、缺省归一与 opts 覆盖、Checker 透传。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oekit/concurrency/breaker"
)

// cancelingModel 模拟「用户在调用中途取消」：进入内层即取消本次调用的 ctx，
// 再返回错误——装饰器侧 AllowGen 已放行、回填时 ctx 已取消，走 Abandon 路径。
type cancelingModel struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	calls  int
}

func (c *cancelingModel) setCancel(f context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = f
}

func (c *cancelingModel) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *cancelingModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	c.mu.Lock()
	cancel := c.cancel
	c.calls++
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil, errors.New("canceling: 中途取消后失败")
}

func (c *cancelingModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("canceling: stream 未实现")
}

func (c *cancelingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return c, nil
}

func TestBreakerModelGenerateForwards(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{bigTool(schema.Assistant, 5)}}
	bm := NewBreakerModel(inner, "m", 3, time.Minute)
	out, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil || out == nil {
		t.Fatalf("Generate: %v", err)
	}
	if inner.callCount() != 1 {
		t.Fatalf("应恰好一次调用，got %d", inner.callCount())
	}
}

// TestBreakerModelTripsAfterConsecutiveFailures 连续失败计数触发：trip=2，
// 连挂两次切 open，之后快速失败（不触达内层），错误可 errors.Is 判
// breaker.ErrCircuitOpen。
func TestBreakerModelTripsAfterConsecutiveFailures(t *testing.T) {
	errBoom := errors.New("boom")
	inner := &seqModel{ // resps 与 errs 等长填充：seqModel 的下标钳制以 resps 长度为准
		resps: []*schema.Message{bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5)},
		errs:  []error{errBoom, errBoom},
	}
	bm := NewBreakerModel(inner, "m", 2, time.Minute)
	for range 2 {
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, errBoom) {
			t.Fatalf("前两次应透传内层错误, got %v", err)
		}
	}
	if bm.st.State() != breaker.StateOpen {
		t.Fatalf("连挂 %d 次应切 open, got %s", 2, bm.st.State())
	}
	_, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if !errors.Is(err, breaker.ErrCircuitOpen) {
		t.Fatalf("开闸期应快速失败 ErrCircuitOpen, got %v", err)
	}
	if inner.callCount() != 2 {
		t.Fatalf("熔断拒绝不应触达内层, got %d", inner.callCount())
	}
}

// TestBreakerModelCooldownHalfOpen 冷却与半开语义（synctest 假时钟）：trip=1
// 一次失败即开闸；虚拟时钟推进过冷却期后放行半开探测，探测成功关闸、恢复放行。
func TestBreakerModelCooldownHalfOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		errBoom := errors.New("boom")
		inner := &seqModel{
			resps: []*schema.Message{bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5)},
			errs:  []error{errBoom},
		}
		bm := NewBreakerModel(inner, "m", 1, time.Minute)

		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, errBoom) {
			t.Fatalf("首次应透传内层错误, got %v", err)
		}
		if bm.st.State() != breaker.StateOpen {
			t.Fatalf("trip=1 失败后应 open, got %s", bm.st.State())
		}
		// 冷却期内拒绝（虚拟时钟未动，forcePass 1s 间隔亦未到）
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
			t.Fatalf("冷却期内应拒绝, got %v", err)
		}
		if inner.callCount() != 1 {
			t.Fatalf("冷却期内不应触达内层, got %d", inner.callCount())
		}
		// 虚拟时钟推进过冷却期 → 放行半开探测；探测成功 → 关闸
		time.Sleep(2 * time.Minute)
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err != nil {
			t.Fatalf("冷却期满应放行半开探测: %v", err)
		}
		if bm.st.State() != breaker.StateClosed {
			t.Fatalf("探测成功应关闸, got %s", bm.st.State())
		}
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err != nil {
			t.Fatalf("关闸后应正常放行: %v", err)
		}
		if inner.callCount() != 3 {
			t.Fatalf("应恰好三次调用（失败/探测成功/关闸后），got %d", inner.callCount())
		}
	})
}

// TestBreakerModelCooldownProbeFailReopens 探测失败重开并重置冷却：冷却期满
// 放行的探测若失败，回到 open 且冷却/forcePass 时钟从头计（重开瞬间立即再拒）。
// 注：open 态内核另有 forcePass 通道（缺省 1s 一次强制探活），冷却中途的放行
// 是 forcePass 而非半开探测——两者用「成功是否关闸」区分（forcePass 成功只进
// 窗口统计，半开探测成功才驱动关闸），本测试全程失败，只钉重开与再放探测。
func TestBreakerModelCooldownProbeFailReopens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		errBoom := errors.New("boom")
		inner := &seqModel{
			resps: []*schema.Message{
				bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5),
				bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5),
			},
			errs: []error{errBoom, errBoom, errBoom},
		}
		bm := NewBreakerModel(inner, "m", 1, time.Minute)

		_, _ = bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}) // trip
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
			t.Fatalf("开闸瞬间应拒绝（冷却与 forcePass 均未到）: %v", err)
		}
		time.Sleep(2 * time.Minute) // 过冷却 → 半开探测
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, errBoom) {
			t.Fatalf("探测应真打内层（透传失败）, got %v", err)
		}
		if bm.st.State() != breaker.StateOpen {
			t.Fatalf("探测失败应重开, got %s", bm.st.State())
		}
		// 重开重置时钟：立即再拒（若时钟未重置，forcePass 1s 间隔早已到，会放探活）
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
			t.Fatalf("重开瞬间应拒绝, got %v", err)
		}
		time.Sleep(2 * time.Minute) // 凑满完整冷却期 → 新探测
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, errBoom) {
			t.Fatalf("冷却期满应放新探测, got %v", err)
		}
		if bm.st.State() != breaker.StateOpen {
			t.Fatalf("第二次探测失败应再重开, got %s", bm.st.State())
		}
	})
}

// TestBreakerModelWithToolsSharesBreakerState issue #198 核心契约：WithTools
// 派生共享状态——多路派生与原实例共享同一熔断状态，连挂计数跨派生累积；
// 「派生即新状态」= 熔断失效（每轮一枚新熔断器，永远凑不齐 trip）。
func TestBreakerModelWithToolsSharesBreakerState(t *testing.T) {
	errBoom := errors.New("boom")
	inner := &seqModel{
		resps: []*schema.Message{bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5)},
		errs:  []error{errBoom, errBoom, errBoom},
	}
	base := NewBreakerModel(inner, "m", 2, time.Minute)

	d1, err := base.WithTools([]*schema.ToolInfo{})
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}
	d2, err := base.WithTools([]*schema.ToolInfo{})
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}
	dm1, dm2 := d1.(*BreakerModel), d2.(*BreakerModel)
	if dm1.st != dm2.st || dm1.st != base.st {
		t.Fatal("派生实例必须原样持有同一熔断状态指针")
	}

	in := []*schema.Message{bigTool(schema.User, 10)}
	// 两个派生实例各挂一次：跨派生累积到 trip=2
	if _, err := dm1.Generate(context.Background(), in); !errors.Is(err, errBoom) {
		t.Fatalf("派生实例 1 应透传失败: %v", err)
	}
	if _, err := dm2.Generate(context.Background(), in); !errors.Is(err, errBoom) {
		t.Fatalf("派生实例 2 应透传失败: %v", err)
	}
	if inner.callCount() != 2 {
		t.Fatalf("应恰好两次真实调用, got %d", inner.callCount())
	}
	// 第三路（原实例）被熔断拒绝——状态确实共享
	if _, err := base.Generate(context.Background(), in); !errors.Is(err, breaker.ErrCircuitOpen) {
		t.Fatalf("跨派生连挂应推动共享熔断状态跳闸, got %v", err)
	}
	// 已派生的旧实例同样被拒（它们与原实例同一状态）
	if _, err := dm1.Generate(context.Background(), in); !errors.Is(err, breaker.ErrCircuitOpen) {
		t.Fatalf("派生实例应与原实例同一熔断状态, got %v", err)
	}
	if inner.callCount() != 2 {
		t.Fatalf("熔断拒绝不应触达内层, got %d", inner.callCount())
	}
}

// TestBreakerModelUserCancelAbandonsNotFails 用户主动中止不计失败（port.Breakers
// 同款纪律）：调用中途取消走 AbandonGen——连续用户取消不把模型推向熔断。
func TestBreakerModelUserCancelAbandonsNotFails(t *testing.T) {
	inner := &cancelingModel{}
	bm := NewBreakerModel(inner, "m", 2, time.Minute)

	// 两次「调用中途用户取消」：结果未知 → Abandon（不计失败）
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		inner.setCancel(cancel)
		if _, err := bm.Generate(ctx, []*schema.Message{bigTool(schema.User, 10)}); err == nil {
			t.Fatal("中途取消应透传内层错误")
		}
	}
	// 之后两次真实失败才凑满 trip=2（若取消被计失败，第一次真实失败前就该跳闸）
	for range 2 {
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err == nil || errors.Is(err, breaker.ErrCircuitOpen) {
			t.Fatalf("真实失败应透传（且前两次取消不计失败）, got %v", err)
		}
	}
	if bm.st.State() != breaker.StateOpen {
		t.Fatalf("两次真实失败应跳闸, got %s", bm.st.State())
	}
	if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
		t.Fatalf("跳闸后应拒绝, got %v", err)
	}
	if inner.callCount() != 4 {
		t.Fatalf("应恰好四次真实调用（2 取消 + 2 失败），got %d", inner.callCount())
	}
}

func TestBreakerModelStreamOutcome(t *testing.T) {
	t.Run("干净EOF记成功", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			inner := &streamFake{chunks: 2}
			bm := NewBreakerModel(inner, "m", 1, time.Minute) // trip=1：EOF 若被计失败，第二条流就该被拒
			for range 2 {
				sr, err := bm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
				if err != nil {
					t.Fatalf("干净流不应推动熔断: %v", err)
				}
				drainStream(t, sr)
				synctest.Wait()
			}
		})
	})
	t.Run("流中错误记失败", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			inner := &streamFake{chunks: 1, failAt: 1}
			bm := NewBreakerModel(inner, "m", 1, time.Minute)
			sr, err := bm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if _, err := sr.Recv(); err != nil {
				t.Fatalf("首分片应到达: %v", err)
			}
			if _, err := sr.Recv(); err == nil {
				t.Fatal("错误帧应透传消费方")
			}
			sr.Close()      // eino 契约：出错退出也须恰好 Close 一次
			synctest.Wait() // 回填在转发 goroutine 内，等它收口
			if bm.st.State() != breaker.StateOpen {
				t.Fatalf("流中错误应计失败并跳闸, got %s", bm.st.State())
			}
			if _, err := bm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
				t.Fatalf("跳闸后流式同样拒绝, got %v", err)
			}
			if inner.callCount() != 1 {
				t.Fatalf("拒绝不应触达内层, got %d", inner.callCount())
			}
		})
	})
	t.Run("消费方提前收流不计失败", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			inner := &streamFake{chunks: 3}
			bm := NewBreakerModel(inner, "m", 1, time.Minute) // trip=1：提前收流若被计失败，下一条流就该被拒
			sr, err := bm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if _, err := sr.Recv(); err != nil {
				t.Fatalf("首分片应到达: %v", err)
			}
			sr.Close() // 消费方拿到即走
			synctest.Wait()
			sr2, err := bm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
			if err != nil {
				t.Fatalf("提前收流（结果未知）不应计失败: %v", err)
			}
			sr2.Close() // 第二条流不消费：读者关流即收口（relay 走 Abandon 退出，bubble 无悬挂 goroutine）
			synctest.Wait()
		})
	})
}

// TestBreakerModelStreamEstablishErrorCountsFailure 流建立即失败（内层 Stream
// 返回错误）同样计失败并回填。
func TestBreakerModelStreamEstablishErrorCountsFailure(t *testing.T) {
	inner := &seqModel{} // seqModel.Stream 恒返回错误
	bm := NewBreakerModel(inner, "m", 1, time.Minute)
	if _, err := bm.Stream(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err == nil {
		t.Fatal("内层 Stream 建立失败应透传错误")
	}
	if bm.st.State() != breaker.StateOpen {
		t.Fatalf("流建立失败应计失败并跳闸, got %s", bm.st.State())
	}
	if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
		t.Fatalf("跳闸后应拒绝, got %v", err)
	}
	if inner.callCount() != 0 {
		t.Fatalf("建立失败不应计入 Generate 调用, got %d", inner.callCount())
	}
}

// TestBreakerModelDefaultsNormalize trip/cooldown ≤0 取家族缺省（3 次/5min，
// 与 ResilientConfig.fillDefaults 同口径）：漏配时保护默认开启而非静默关闭。
func TestBreakerModelDefaultsNormalize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		errBoom := errors.New("boom")
		inner := &seqModel{
			resps: []*schema.Message{
				bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5),
				bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5),
			},
			errs: []error{errBoom, errBoom, errBoom},
		}
		bm := NewBreakerModel(inner, "m", 0, 0)
		for range 3 {
			if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, errBoom) {
				t.Fatalf("缺省 trip=3：第 %d 次失败应透传, got %v", 3, err)
			}
		}
		if bm.st.State() != breaker.StateOpen {
			t.Fatalf("缺省 trip=3 三次连挂应跳闸, got %s", bm.st.State())
		}
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
			t.Fatalf("跳闸后应拒绝, got %v", err)
		}
		// 缺省 cooldown=5min（非内核回落缺省 1min）：4min 处（> forcePass 1s 间隔、
		// < 5min 冷却）放行的只能是 forcePass 探活——成功不驱动关闸；若缺省冷却
		// 误为 1min，此次放行将是半开探测，成功即关闸，状态可区分
		time.Sleep(4 * time.Minute)
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err != nil {
			t.Fatalf("forcePass 探活应真打内层: %v", err)
		}
		if bm.st.State() != breaker.StateOpen {
			t.Fatalf("forcePass 成功不应关闸（若已关闸说明缺省冷却误为 1min）, got %s", bm.st.State())
		}
		time.Sleep(2 * time.Minute) // 凑满 5min 冷却 → 半开探测，成功关闸
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); err != nil {
			t.Fatalf("缺省冷却期满应放行探测: %v", err)
		}
		if bm.st.State() != breaker.StateClosed {
			t.Fatalf("探测成功应关闸, got %s", bm.st.State())
		}
	})
}

// TestBreakerModelOptsOverrideBase opts 后置追加可覆盖基线：显式
// WithConsecutiveFailures(5) 时基线 trip=3 不生效。
func TestBreakerModelOptsOverrideBase(t *testing.T) {
	errBoom := errors.New("boom")
	inner := &seqModel{
		resps: []*schema.Message{bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5), bigTool(schema.Assistant, 5)},
		errs:  []error{errBoom, errBoom, errBoom, errBoom, errBoom},
	}
	bm := NewBreakerModel(inner, "m", 3, time.Minute, breaker.WithConsecutiveFailures(5))
	for range 5 {
		if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, errBoom) {
			t.Fatalf("opts 覆盖后 5 次内不应跳闸, got %v", err)
		}
	}
	if bm.st.State() != breaker.StateOpen {
		t.Fatalf("连挂 5 次应按 opts 阈值跳闸, got %s", bm.st.State())
	}
	if _, err := bm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)}); !errors.Is(err, breaker.ErrCircuitOpen) {
		t.Fatalf("跳闸后应拒绝, got %v", err)
	}
	if inner.callCount() != 5 {
		t.Fatalf("应恰好五次真实调用, got %d", inner.callCount())
	}
}

func TestBreakerModelCheckerPassthrough(t *testing.T) {
	inner := &checkerInner{fakeChatModel: &fakeChatModel{}, enabled: true}
	bm := NewBreakerModel(inner, "m", 3, time.Minute)
	if !bm.IsCallbacksEnabled() {
		t.Fatal("应透传内层 enabled=true")
	}
	inner.enabled = false
	if bm.IsCallbacksEnabled() {
		t.Fatal("应透传内层 enabled=false")
	}
	plain := NewBreakerModel(&fakeChatModel{}, "m", 3, time.Minute)
	if plain.IsCallbacksEnabled() {
		t.Fatal("内层不实现 Checker 时应为 false")
	}
}
