package llm

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/oagentkit/llm/internal/fitkit"
)

// #202：fitInput 对 ToolCalls 主导的超限此前只能硬失败（参数 JSON 不改写、
// 无可裁正文）——L4 组丢弃兜底后应能救回：会话收敛入窗、丢弃注记留痕可审计。
func TestFitInputDropsToolGroups_Issue202(t *testing.T) {
	c := &Client{ContextTokens: 1300, MaxOutputTokens: 10}
	msgList := []*schema.Message{
		schema.UserMessage(strings.Repeat("问", 100)),
	}
	for i := range 20 {
		id := "c" + string(rune('a'+i))
		msgList = append(msgList,
			&schema.Message{Role: schema.Assistant, Content: "",
				ToolCalls: []schema.ToolCall{{ID: id, Function: schema.FunctionCall{
					Name: "do", Arguments: `{"q":"` + strings.Repeat("参", 200) + `"}`}}}},
			&schema.Message{Role: schema.Tool, Content: "OK", ToolCallID: id},
		)
	}
	if _, err := c.fitInput(msgList); err != nil {
		t.Fatalf("组丢弃兜底后应收敛入窗（此前硬失败）: %v", err)
	}
	// 注记留痕：丢弃位置出现 assistant 注记。
	noteSeen := false
	for _, m := range msgList {
		if strings.Contains(m.Content, fitkit.GroupDropMarker) {
			noteSeen = true
		}
	}
	if !noteSeen {
		t.Fatal("丢弃注记未出现（置换行为必须可审计）")
	}
	// 配对一致性：无孤儿调用。
	calls := map[string]bool{}
	for _, m := range msgList {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
	}
	for _, m := range msgList {
		if m.Role == schema.Tool && m.ToolCallID != "" {
			delete(calls, m.ToolCallID)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("组丢弃产生孤儿调用: %v", calls)
	}
}

// #202 保护面：携带 tool-error 的轮组不丢，丢弃从最早的非保护组开始。
func TestFitInputGroupDropProtectsErrorResults_Issue202(t *testing.T) {
	c := &Client{ContextTokens: 1300, MaxOutputTokens: 10}
	msgList := []*schema.Message{
		schema.UserMessage(strings.Repeat("问", 100)),
	}
	for i := range 20 {
		id := "c" + string(rune('a'+i))
		toolContent := "OK"
		if i == 0 {
			toolContent = "<system-reminder>关键自纠信息</system-reminder>"
		}
		msgList = append(msgList,
			&schema.Message{Role: schema.Assistant, Content: "",
				ToolCalls: []schema.ToolCall{{ID: id, Function: schema.FunctionCall{
					Name: "do", Arguments: `{"q":"` + strings.Repeat("参", 200) + `"}`}}}},
			&schema.Message{Role: schema.Tool, Content: toolContent, ToolCallID: id},
		)
	}
	if _, err := c.fitInput(msgList); err != nil {
		t.Fatalf("应丢弃非保护组收敛: %v", err)
	}
	found := false
	for _, m := range msgList {
		if m.Role == schema.Tool && strings.Contains(m.Content, "关键自纠信息") {
			found = true
		}
	}
	if !found {
		t.Fatal("保护组的提醒结果不得被丢弃")
	}
}
