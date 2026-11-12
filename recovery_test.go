package llm

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestIsTransientStreamError(t *testing.T) {
	transient := []error{
		errors.New("stream_idle_timeout after 30s"),
		errors.New("code=model_rate_limited"),
		errors.New("MODEL_NETWORK_ERROR: connection reset"),
		errors.New("server_error: 502"),
	}
	for _, e := range transient {
		if !IsTransientStreamError(e) {
			t.Errorf("应判瞬态: %v", e)
		}
	}
	permanent := []error{
		errors.New("invalid api key"),
		errors.New("model not found: foo"),
		nil,
	}
	for _, e := range permanent {
		if IsTransientStreamError(e) {
			t.Errorf("不应判瞬态: %v", e)
		}
	}
}

func TestSuspiciousEmptyResult(t *testing.T) {
	cases := []struct {
		name   string
		finish string
		text   int
		calls  int
		usage  int
		want   bool
	}{
		{"全空+长度截断", "length", 0, 0, 0, true},
		{"全空+未知 finish", "", 0, 0, 0, true},
		{"有文本", "length", 5, 0, 0, false},
		{"有工具调用", "tool_calls", 0, 1, 100, false},
		{"正常 stop", "stop", 0, 0, 0, false},
		{"零内容但有用量", "length", 0, 0, 500, false},
	}
	for _, tc := range cases {
		if got := SuspiciousEmptyResult(tc.finish, tc.text, tc.calls, tc.usage); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestContextExceeded(t *testing.T) {
	if !IsContextExceededFinish("context_length_exceeded", "") {
		t.Error("finishReason 形态应识别")
	}
	if !IsContextExceededFinish("", "Prompt_Too_Long") {
		t.Error("raw 形态（大小写归一）应识别")
	}
	if IsContextExceededFinish("stop", "") {
		t.Error("stop 不应误判")
	}
	if !IsContextExceededError(errors.New("this model context_window_exceeded 128000")) {
		t.Error("错误文案应识别")
	}
	if IsContextExceededError(errors.New("normal error")) {
		t.Error("普通错误不应误判")
	}
}

func TestBusyAdmission(t *testing.T) {
	for _, code := range []string{"3008", "3009", "3010", " 3010 "} {
		if !IsProviderBusyCode(code) {
			t.Errorf("%s 应判 busy", code)
		}
	}
	if IsProviderBusyCode("429") {
		t.Error("429 是限流不是 busy")
	}
	if BusyAdmissionDelay(0) != time.Second || BusyAdmissionDelay(1) != 2*time.Second {
		t.Error("两档退避 1s/2s")
	}
	if BusyAdmissionDelay(99) != 2*time.Second {
		t.Error("越界取最后一档")
	}
	if MaxStreamRecoveryRetries != 10 {
		t.Error("恢复预算常量")
	}
}

// TestBusyAdmissionDelayEmptyTableRepro_Issue130 复现 llm#130：导出的
// BusyAdmissionDelays var 被置空后取档——修复前 `retry >= len(t)` ⇒
// `retry = len(t)-1` 把下标算成 -1，`index out of range [-1]` panic（发生在
// LLM 重试路径上）。修复后热路径读 SetBusyAdmissionDelays 维护的原子快照，
// 直写导出 var 与热路径解耦（也不再有写读 data race，oagentkit/agentkit#167）
// ——本测试的置空因此不再影响取值，取到的仍是缺省快照 1s/2s。
func TestBusyAdmissionDelayEmptyTableRepro_Issue130(t *testing.T) {
	orig := BusyAdmissionDelays
	t.Cleanup(func() { BusyAdmissionDelays = orig })
	BusyAdmissionDelays = nil // 导出 var，任何调用方可置空（issue 复现形态）

	// 修复前此处 panic；修复后与热路径解耦，返回缺省快照。
	if got := BusyAdmissionDelay(0); got != time.Second {
		t.Fatalf("直写 var 已解耦，应取缺省快照第一档 1s，got %v", got)
	}
	if got := BusyAdmissionDelay(99); got != 2*time.Second {
		t.Fatalf("直写 var 已解耦，应取缺省快照最后一档 2s，got %v", got)
	}
}

// TestBusyAdmissionDelayEmptySnapshotFailsafe_Issue130 空表 fail-safe 分支
// （llm#130）：生效退避表为空（nil/空切片）时返回 0——「空表 = 不退避」，
// 任何 retry 序号都不得把下标算成 -1 越界。快照正常入口（SetBusyAdmissionDelays）
// 拒绝空表，本分支是取值函数的自保护（白盒注入验证）。
func TestBusyAdmissionDelayEmptySnapshotFailsafe_Issue130(t *testing.T) {
	orig := busyAdmissionTable.Load()
	t.Cleanup(func() { busyAdmissionTable.Store(orig) })

	for name, tbl := range map[string]*[]time.Duration{
		"nil 表": nil,
		"空切片":   {},
	} {
		busyAdmissionTable.Store(tbl)
		for _, retry := range []int{-1, 0, 1, 99} {
			if got := BusyAdmissionDelay(retry); got != 0 {
				t.Fatalf("%s: BusyAdmissionDelay(%d) 应 fail-safe 返回 0，got %v", name, retry, got)
			}
		}
	}
}

// TestSetBusyAdmissionDelays_Issue130 钉 setter 契约（llm#130）：空表拒绝、
// 越界取最后一档、负序号取第一档、入参防御性拷贝（调用方此后改动切片不再
// 影响已生效的表）。
func TestSetBusyAdmissionDelays_Issue130(t *testing.T) {
	orig := busyAdmissionTable.Load()
	t.Cleanup(func() { busyAdmissionTable.Store(orig) })

	// 空表拒绝（与 oagentkit/agentkit 门面 setter 同口径下沉）。
	if err := SetBusyAdmissionDelays(nil); !errors.Is(err, ErrEmptyBusyAdmissionDelays) {
		t.Fatalf("nil 表应报 ErrEmptyBusyAdmissionDelays，got %v", err)
	}
	if err := SetBusyAdmissionDelays([]time.Duration{}); !errors.Is(err, ErrEmptyBusyAdmissionDelays) {
		t.Fatalf("空切片应报 ErrEmptyBusyAdmissionDelays，got %v", err)
	}

	// 正常设置 + 夹取语义。
	tbl := []time.Duration{3 * time.Second, 7 * time.Second, 11 * time.Second}
	if err := SetBusyAdmissionDelays(tbl); err != nil {
		t.Fatalf("SetBusyAdmissionDelays: %v", err)
	}
	if got := BusyAdmissionDelay(0); got != 3*time.Second {
		t.Fatalf("第一档应 3s，got %v", got)
	}
	if got := BusyAdmissionDelay(-1); got != 3*time.Second {
		t.Fatalf("负序号应取第一档，got %v", got)
	}
	if got := BusyAdmissionDelay(2); got != 11*time.Second {
		t.Fatalf("第三档应 11s，got %v", got)
	}
	if got := BusyAdmissionDelay(99); got != 11*time.Second {
		t.Fatalf("越界应取最后一档 11s，got %v", got)
	}

	// 防御性拷贝：setter 返回后改写入参元素/追加，已生效的表不受影响。
	tbl[0] = 99 * time.Second
	_ = append(tbl, 55*time.Second) // 追加只作用于入参侧（弃返回值；linter：赋回未再读 = ineffectual）
	if got := BusyAdmissionDelay(0); got != 3*time.Second {
		t.Fatalf("入参元素改写不应影响已生效的表，got %v", got)
	}
	if got := BusyAdmissionDelay(3); got != 11*time.Second {
		t.Fatalf("入参 append 不应影响已生效的表（仍 3 档，末档 11s），got %v", got)
	}
}

// TestBusyAdmissionDelayConcurrentSwap_Issue130 并发写读不构成 data race
// （llm#130 / oagentkit/agentkit#167 的上游收口）：读热路径（BusyAdmissionDelay）
// 与原子替换（SetBusyAdmissionDelays）并发时，读者取到的任一快照都须完整
// 有效（元素值 ∈ 已设置过的表）。-race 下跑。
func TestBusyAdmissionDelayConcurrentSwap_Issue130(t *testing.T) {
	orig := busyAdmissionTable.Load()
	t.Cleanup(func() { busyAdmissionTable.Store(orig) })

	tables := [][]time.Duration{
		{10 * time.Millisecond, 20 * time.Millisecond},
		{30 * time.Millisecond, 40 * time.Millisecond, 50 * time.Millisecond},
		{60 * time.Millisecond},
	}
	valid := map[time.Duration]bool{
		10 * time.Millisecond: true, 20 * time.Millisecond: true,
		30 * time.Millisecond: true, 40 * time.Millisecond: true,
		50 * time.Millisecond: true, 60 * time.Millisecond: true,
	}
	// 起读者前先落一张已知表：缺省快照（1s/2s）不在 valid 集内，避免读到
	// 缺省值被误判（读者启动到首次替换之间的合法可见值）。
	if err := SetBusyAdmissionDelays(tables[0]); err != nil {
		t.Fatalf("SetBusyAdmissionDelays: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 读者：并发取档（模拟重试热路径）。
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				for retry := range 5 {
					if d := BusyAdmissionDelay(retry); !valid[d] {
						t.Errorf("读到无效档位 %v（撕裂/越界）", d)
						return
					}
				}
			}
		})
	}
	// 写者：原子替换（模拟门面 setter 调优）。
	wg.Go(func() {
		for i := range 200 {
			if err := SetBusyAdmissionDelays(tables[i%len(tables)]); err != nil {
				t.Errorf("SetBusyAdmissionDelays: %v", err)
				return
			}
		}
		close(stop)
	})
	wg.Wait()
}
