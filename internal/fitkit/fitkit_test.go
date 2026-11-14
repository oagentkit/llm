package fitkit

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// #201 漂移回归钉：保护判定的语义面（两种信封 + 负例）。
func TestIsProtectedResult(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"<system-reminder>提醒</system-reminder>", true},
		{"前缀【tool-error】后缀", true},
		{"普通工具结果", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsProtectedResult(tc.in); got != tc.want {
			t.Fatalf("IsProtectedResult(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// 组划分：assistant(带 ToolCalls) 本体入组首位、其后连续 Tool 归组；
// 孤立工具结果不入组。
func TestToolGroups(t *testing.T) {
	msgs := []*schema.Message{
		schema.UserMessage("问"),
		{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "f", Arguments: "{}"}}}},
		{Role: schema.Tool, Content: "r1"},
		{Role: schema.Tool, Content: "r2"},
		schema.AssistantMessage("后续", nil),
		{Role: schema.Tool, Content: "孤立结果"},
	}
	gs := ToolGroups(msgs)
	if len(gs) != 1 {
		t.Fatalf("组数 = %d, want 1", len(gs))
	}
	// 组成员 = assistant 本体 + 两个结果
	if len(gs[0]) != 3 || msgs[gs[0][0]].Role != schema.Assistant ||
		msgs[gs[0][1]].Role != schema.Tool || msgs[gs[0][2]].Role != schema.Tool {
		t.Fatalf("组成员形态不对: %v", gs[0])
	}
	if GroupProtected(msgs, gs[0]) {
		t.Fatal("无保护内容的组不应判保护")
	}
}

// 组内任一成员携带提醒信封/tool-error 即整组保护。
func TestGroupProtected(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "f", Arguments: "{}"}}}},
		{Role: schema.Tool, Content: "正常"},
		{Role: schema.Tool, Content: strings.Repeat("x", 10) + "【tool-error】"},
	}
	gs := ToolGroups(msgs)
	if len(gs) != 1 || !GroupProtected(msgs, gs[0]) {
		t.Fatalf("含 tool-error 的组应整组保护: %v", gs)
	}
}
