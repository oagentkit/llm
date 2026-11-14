package wrap

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/internal/fitkit"
)

// makeToolRound 构造一轮工具调用：assistant 大参数 ToolCalls + 小结果。
func makeToolRound(id string, argRunes, resultRunes int) []*schema.Message {
	call := schema.ToolCall{ID: id, Function: schema.FunctionCall{
		Name: "do", Arguments: `{"q":"` + strings.Repeat("参", argRunes) + `"}`}}
	return []*schema.Message{
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{call}},
		{Role: schema.Tool, Content: strings.Repeat("果", resultRunes), ToolCallID: id},
	}
}

// #200：assistant 大 ToolCalls（参数 JSON 占大头）的会话此前在 wrap 侧无可裁
// 对象、FitPass 直通；L4 组丢弃兜底后必须得到可裁结果或显式丢弃注记。
// 参数取值：轮参数 200 rune 主导（结果 50 rune 很小），keep-recent-5 窗口
// ≈1100 rune 可装下——超限完全由更早的轮组贡献，丢弃即收敛。
func TestFitMessagesDropsToolCallGroups_Issue200(t *testing.T) {
	var msgs []*schema.Message
	msgs = append(msgs, schema.UserMessage(strings.Repeat("问", 100)))
	for i := range 20 {
		msgs = append(msgs, makeToolRound(
			"c"+string(rune('a'+i)), 200, 50)...)
	}
	limit := 1500
	out, res := FitMessages(msgs, limit)
	if res.Action == FitPass {
		t.Fatal("ToolCalls 主导超限不得再 FitPass 直通（#200）")
	}
	if res.Dropped == 0 {
		t.Fatalf("应发生组丢弃: %+v", res)
	}
	if res.After > limit {
		t.Fatalf("组丢弃后应入窗: %d > %d", res.After, limit)
	}
	// 注记留痕可审计：被丢弃轮的位置出现 GroupDropNote。
	noteSeen := false
	for _, m := range out {
		if strings.Contains(m.Content, fitkit.GroupDropMarker) {
			noteSeen = true
			if m.Role != schema.Assistant {
				t.Fatal("组丢弃注记应为 assistant 消息")
			}
		}
	}
	if !noteSeen {
		t.Fatal("丢弃注记未出现")
	}
	// 配对一致性：任何 assistant 的 ToolCall都必须能在后继 Tool 消息找到
	// 同 ID 的结果（组粒度移除不得产生孤儿）。
	calls := map[string]bool{}
	for _, m := range out {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
	}
	for _, m := range out {
		if m.Role == schema.Tool && m.ToolCallID != "" && calls[m.ToolCallID] {
			delete(calls, m.ToolCallID)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("存在无配对结果的孤儿调用: %v", calls)
	}
}

// #200 保护面：组内结果携带提醒信封/tool-error 的轮组不丢。
func TestFitMessagesGroupDropProtectsErrorResults_Issue200(t *testing.T) {
	var msgs []*schema.Message
	msgs = append(msgs, schema.UserMessage(strings.Repeat("问", 100)))
	for i := range 20 {
		r := makeToolRound("c"+string(rune('a'+i)), 200, 50)
		if i == 0 {
			r[1].Content = "<system-reminder>任务规则</system-reminder>"
		}
		msgs = append(msgs, r...)
	}
	out, res := FitMessages(msgs, 1500)
	if res.After > 8000 {
		t.Fatalf("应通过丢其他组入窗: %+v", res)
	}
	// 受保护组必须仍在（结果原文未被丢）。
	found := false
	for _, m := range out {
		if m.Role == schema.Tool && strings.Contains(m.Content, "任务规则") {
			found = true
		}
	}
	if !found {
		t.Fatal("保护组的提醒结果不得被组丢弃")
	}
}
