package llm

// 2026-11-14 issue 清零战回归测试（#186-#197，#198 下沉提案不在本车道）：
// 每条用例钉一个失效形态，标题带 issue 号——回归时按号检索。
// #186/#191(port 侧) 的用例在 port 包，#196 在 llmtest 包，#187(wrap 侧) 在 wrap 包。

import (
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

// --- #187 fit 裁剪与记账同字段面（Content + ToolCalls + 多模态文本部件） ---

// TestMsgRuneCountCoversToolCallsAndMultiContent_Issue187 单源计数面：
// 大入参 ToolCalls 与多模态文本部件必须计入——与 estimateMsgTokens（#139）
// 成对，裁剪与记账两套出口同一口径。
func TestMsgRuneCountCoversToolCallsAndMultiContent_Issue187(t *testing.T) {
	args := `{"query":"` + strings.Repeat("v", 100) + `"}`
	m := &schema.Message{
		Role:      schema.Assistant,
		Content:   "",
		ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "search", Arguments: args}}},
	}
	want := utf8Len("search") + utf8Len(args)
	if got := MsgRuneCount(m); got != want {
		t.Fatalf("MsgRuneCount 应计入工具名+参数 JSON：got %d want %d", got, want)
	}
	mm := &schema.Message{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: strings.Repeat("图", 50)}}}
	if got := MsgRuneCount(mm); got != 50 {
		t.Fatalf("多模态文本部件应计入: %d", got)
	}
	if MsgRuneCount(nil) != 0 {
		t.Fatal("nil 消息应为 0")
	}
}

func utf8Len(s string) int { return len([]rune(s)) }

// TestFitInputSeesToolCalls_Issue187 ToolCalls 占大头时 fit 必须触发：
// Content-only 口径下 total ≤ limit 直通（旧 bug），全字段口径下必须裁剪，
// 且 ToolCalls 本体不被改写（协议配对完整性优先）。
func TestFitInputSeesToolCalls_Issue187(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10} // limit = 121 rune
	user := strings.Repeat("你", 100)
	args := `{"k":"` + strings.Repeat("v", 31) + `"}`
	msgList := []*schema.Message{
		schema.UserMessage(user),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "f", Arguments: args}}}},
	}
	if err := c.fitInput(msgList); err != nil {
		t.Fatalf("user 截断后应入窗: %v", err)
	}
	if utf8Len(msgList[0].Content) >= 100 {
		t.Fatal("ToolCalls 占大头时 user 消息应被裁剪（旧口径只数 Content 会误判未超限直通）")
	}
	if !strings.Contains(msgList[0].Content, "已截断留痕") {
		t.Fatal("截断应留痕")
	}
	if msgList[1].ToolCalls[0].Function.Arguments != args {
		t.Fatal("ToolCalls 参数不得被改写")
	}
}

// --- #188 fitInput 无 user 兜底、回环裁剪、不可收敛给可诊断错误 ---

// TestFitInputWithoutUserFallsBackToTool_Issue188 纯 system/tool 会话超限：
// 此前 longest<0 一行不改静默直通；修复后回环截最长可裁正文。
func TestFitInputWithoutUserFallsBackToTool_Issue188(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10} // limit = 121 rune
	sys := "sys"
	tool := strings.Repeat("果", 200)
	msgList := []*schema.Message{
		schema.SystemMessage(sys),
		{Role: schema.Tool, Content: tool},
	}
	if err := c.fitInput(msgList); err != nil {
		t.Fatalf("tool 正文可裁，应收敛入窗: %v", err)
	}
	if utf8Len(msgList[1].Content) >= 200 {
		t.Fatal("无 user 会话应退化为截最长 tool 正文")
	}
	if msgList[0].Content != sys {
		t.Fatal("system 提示词不裁")
	}
	if MsgsRuneCount(msgList) > 121 {
		t.Fatalf("裁剪后应入窗: %d", MsgsRuneCount(msgList))
	}
}

// TestFitInputLoopsUntilFit_Issue188 超限量 > 最长 user 长度：此前只裁一条、
// 裁完不复查（user 削成注记、总量仍超限照发）；修复后回环迭代直至入窗。
func TestFitInputLoopsUntilFit_Issue188(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10} // limit = 121 rune
	msgList := []*schema.Message{
		schema.UserMessage(strings.Repeat("问", 200)),
		{Role: schema.Tool, Content: strings.Repeat("果", 200)},
	}
	if err := c.fitInput(msgList); err != nil {
		t.Fatalf("回环裁剪应入窗: %v", err)
	}
	if MsgsRuneCount(msgList) > 121 {
		t.Fatalf("回环后仍超限: %d", MsgsRuneCount(msgList))
	}
	if !strings.Contains(msgList[0].Content, "已截断留痕") ||
		!strings.Contains(msgList[1].Content, "已截断留痕") {
		t.Fatal("user 与 tool 都应被裁并留痕")
	}
}

// TestFitInputUnconvergedIsDiagnosable_Issue188 无可裁对象（仅 system）仍超限：
// 不再静默放行——返回可诊断错误，且被分类成确定性失败（不重试同模型，
// 降级链可切换更大窗口的备选）。
func TestFitInputUnconvergedIsDiagnosable_Issue188(t *testing.T) {
	c := &Client{ContextTokens: 100, MaxOutputTokens: 10}
	sys := strings.Repeat("系统", 250)
	msgList := []*schema.Message{schema.SystemMessage(sys)}
	err := c.fitInput(msgList)
	if err == nil {
		t.Fatal("无可裁对象且超限，应报错而非静默直通")
	}
	if !strings.Contains(err.Error(), "context too long") {
		t.Fatalf("错误应带 context too long marker: %v", err)
	}
	if ClassifyLLMError(err).Retryable {
		t.Fatal("本地可诊断的窗口超限应判确定性失败（不重试）")
	}
	if msgList[0].Content != sys {
		t.Fatal("system 提示词不得被裁")
	}
}
