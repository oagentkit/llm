package wrap

// 2026-11-14 issue 清零战回归（#187 wrap 侧）：FitMessages 的总量计量单源
// llm.MsgRuneCount——ToolCalls（工具名+参数 JSON）与多模态文本部件计入，
// tool-heavy 会话不再「未超限」误判直通。

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestFitMessagesCountsToolCalls_Issue187(t *testing.T) {
	args := `{"k":"` + strings.Repeat("v", 100) + `"}`
	msgs := []*schema.Message{
		schema.UserMessage(strings.Repeat("你", 100)),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "f", Arguments: args}}}},
	}
	// Content-only 总量 = 100 ≤ 150（旧口径直通，bug）；全字段总量 > 150。
	out, res := FitMessages(msgs, 150)
	if res.Action == FitPass {
		t.Fatal("ToolCalls 占大头时 fit 必须触发（旧口径只数 Content 会误判未超限）")
	}
	if MsgsRuneCountOf(out) > 150 {
		t.Fatalf("拟合后应入窗: %d", MsgsRuneCountOf(out))
	}
	if out[1].ToolCalls[0].Function.Arguments != args {
		t.Fatal("ToolCalls 参数不得被改写（协议配对完整性优先，裁剪只动正文）")
	}
}

// MsgsRuneCountOf 测试侧计数（与 llm.MsgRuneCount 同字段面，防 wrap 内部
// 回归计数面漂移的最小护栏）。
func MsgsRuneCountOf(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		n := len([]rune(m.Content))
		for _, tc := range m.ToolCalls {
			n += len([]rune(tc.Function.Name)) + len([]rune(tc.Function.Arguments))
		}
		for _, part := range m.MultiContent {
			n += len([]rune(part.Text))
		}
		total += n
	}
	return total
}
