package obsx

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oekit/logx"
)

// TestDrainStreamUsageSkipsContentAccumulation 回归（oagentkit/llm#173）：
// DrainStreamUsage 只要 usage/modelName/finishReason，却走单源 drainStream
// 无条件累计整条流 Content——逐帧 WriteString + Builder 扩容 + 弃置，O(内容)
// 拷贝压在观测热路径上。现经 accumulateContent=false 全程不碰 Builder。
func TestDrainStreamUsageSkipsContentAccumulation(t *testing.T) {
	const frames = 4
	big := strings.Repeat("内容", 1<<17) // 每帧 ~512KB，累计 ~2MB
	mk := func() *schema.StreamReader[callbacks.CallbackOutput] {
		items := make([]callbacks.CallbackOutput, 0, frames)
		for i := 0; i < frames; i++ {
			items = append(items, &model.CallbackOutput{
				Message: &schema.Message{Role: schema.Assistant, Content: big},
			})
		}
		return schema.StreamReaderFromArray(items)
	}

	// 行为钉：false 路径不产出 content，true 路径照常累计（流式 end 依赖它）。
	if _, _, _, c := drainStream(mk(), false); c != "" {
		t.Fatalf("accumulateContent=false 不应产出 content，得到 %d 字节", len(c))
	}
	if _, _, _, c := drainStream(mk(), true); len(c) != frames*len(big) {
		t.Fatalf("accumulateContent=true 应累计整条流，得到 %d 字节，want %d", len(c), frames*len(big))
	}
	// 导出面仍提取到 usage 语义（last-non-nil 家族不受影响）：帧无 usage 时
	// 三返回值均为零值但**不 panic、不漏帧**。
	if u, name, fr := DrainStreamUsage(mk()); u != nil || name != "" || fr != "" {
		t.Fatalf("纯 content 帧三返回值应为零值: %v %q %q", u, name, fr)
	}

	// 分配钉：DrainStreamUsage 不再持有整条 content——2MB 累计的 Builder
	// 扩容（~log2 次增长分配）必须只出现在 true 路径。
	without := testing.AllocsPerRun(10, func() { DrainStreamUsage(mk()) })
	with := testing.AllocsPerRun(10, func() { drainStream(mk(), true) })
	if without >= with {
		t.Fatalf("DrainStreamUsage 分配(%v) 应少于累计路径(%v)——false 路径仍在拷贝整条 content", without, with)
	}
}

// TestTracingHandlerLenFieldsAlwaysPresent 回归（oagentkit/llm#174）：
// Options.PreviewLen 承诺「无论取值，输入/输出侧恒落 input_len/output_len」，
// 实现却是条件落——nil 输入帧与 out.Message==nil 的输出帧整个键缺席，按
// 「字段恒在」建 schema 的消费方踩空。无内容 = 长度 0，键必须在。
func TestTracingHandlerLenFieldsAlwaysPresent(t *testing.T) {
	t.Run("nil输入帧", func(t *testing.T) {
		rl := &recordLogger{Logger: logx.NewSlogLogger("test")}
		ctx := InitLLMObservability(context.Background(), rl, Options{})
		ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
		callbacks.OnStart(ctx, (*model.CallbackInput)(nil)) // ConvCallbackInput → nil（工具调用帧/归一化失败形态）
		start := rl.find("llm.call.start")
		if start == nil {
			t.Fatalf("缺 llm.call.start，实际 %d 条", rl.count())
		}
		v, ok := start.value("input_len")
		if !ok {
			t.Fatal("nil 输入帧 input_len 键整个缺席（llm#174：承诺恒落）")
		}
		if v != 0 {
			t.Fatalf("nil 输入帧 input_len = %v，want 0", v)
		}
	})

	t.Run("无Message输出帧", func(t *testing.T) {
		rl := &recordLogger{Logger: logx.NewSlogLogger("test")}
		ctx := InitLLMObservability(context.Background(), rl, Options{})
		ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
		ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})
		callbacks.OnEnd(ctx, &model.CallbackOutput{}) // Message nil（工具调用帧形态）
		end := rl.find("llm.call.end")
		if end == nil {
			t.Fatalf("缺 llm.call.end，实际 %d 条", rl.count())
		}
		v, ok := end.value("output_len")
		if !ok {
			t.Fatal("Message==nil 输出帧 output_len 键整个缺席（llm#174：承诺恒落）")
		}
		if v != 0 {
			t.Fatalf("Message==nil 输出帧 output_len = %v，want 0", v)
		}
	})

	t.Run("归一化失败输出帧", func(t *testing.T) {
		rl := &recordLogger{Logger: logx.NewSlogLogger("test")}
		ctx := InitLLMObservability(context.Background(), rl, Options{})
		ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
		ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})
		callbacks.OnEnd(ctx, (*model.CallbackOutput)(nil)) // ConvCallbackOutput → nil
		end := rl.find("llm.call.end")
		if end == nil {
			t.Fatalf("缺 llm.call.end，实际 %d 条", rl.count())
		}
		v, ok := end.value("output_len")
		if !ok {
			t.Fatal("nil 输出帧 output_len 键整个缺席（llm#174：承诺恒落）")
		}
		if v != 0 {
			t.Fatalf("nil 输出帧 output_len = %v，want 0", v)
		}
	})
}
