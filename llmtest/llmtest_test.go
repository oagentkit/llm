package llmtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestModelScriptSemantics(t *testing.T) {
	// 耗尽报错（默认）：脚本写漏立即可见。
	m := &Model{Script: []Resp{{Content: "only"}}}
	if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")}); err == nil ||
		!strings.Contains(err.Error(), "脚本耗尽") {
		t.Fatalf("耗尽应报错: %v", err)
	}

	// RepeatLast：末条重复（重试/降级链测试依赖持续错误）。
	m2 := &Model{RepeatLast: true, Script: []Resp{{Err: errors.New("down")}}}
	for range 3 {
		if _, err := m2.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")}); err == nil {
			t.Fatal("RepeatLast 应持续返回末条错误")
		}
	}

	// Inputs 历史 + ResponseMeta 填充。
	m3 := &Model{Script: []Resp{{Content: "x", FinishReason: "length", Prompt: 10, Completion: 5}}, RepeatLast: true}
	msgs := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		{Role: schema.User, Content: "hello"},
	}
	out, err := m3.Generate(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if out.ResponseMeta == nil || out.ResponseMeta.FinishReason != "length" ||
		out.ResponseMeta.Usage.PromptTokens != 10 || out.ResponseMeta.Usage.TotalTokens != 15 {
		t.Fatalf("ResponseMeta/Usage 填充不符: %+v", out.ResponseMeta)
	}
	if m3.FirstInput != "sys" || m3.LastInput != "hello" || len(m3.Inputs) != 1 || m3.Inputs[0] != "hello" {
		t.Fatalf("输入记录不符: %q %q %v", m3.FirstInput, m3.LastInput, m3.Inputs)
	}
}

func TestModelStream(t *testing.T) {
	m := &Model{}
	if _, err := m.Stream(context.Background(), nil); err == nil {
		t.Fatal("未配置 StreamContent 应报错")
	}
	m.StreamContent = "chunk"
	sr, err := m.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := sr.Recv()
	if err != nil || msg.Content != "chunk" {
		t.Fatalf("Stream 内容不符: %v %q", err, msg.Content)
	}
}

func TestToolModelWithTools(t *testing.T) {
	// WithTools 派生共享父桩状态（脚本继续按序消费）。
	base := &Model{RepeatLast: true, Script: []Resp{{Content: "ok"}}}
	tm := &ToolModel{Model: base}
	derived, err := tm.WithTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := derived.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil || out.Content != "ok" {
		t.Fatalf("派生实例应共享脚本: %v %q", err, out.Content)
	}
}

func TestProviderDefaults(t *testing.T) {
	p := NewScriptedProvider("p1", Resp{Content: "hi"})
	if p.Name() != "p1" || p.ModelName() != "p1-model" {
		t.Fatalf("Name/ModelName = %q/%q", p.Name(), p.ModelName())
	}
	if p.ContextTokens() != 128_000 || p.MaxOutputTokens() != 4096 {
		t.Fatal("窗口缺省不符")
	}
	pc, cc := p.CostPer1KTokens()
	if pc != 0.001 || cc != 0.002 {
		t.Fatalf("成本缺省不符: %v %v", pc, cc)
	}
}

// TestStreamResponseMeta 回归（oagentkit/llm#166）：Stream 此前丢弃 Resp 的
// ResponseMeta（usage + finish_reason）——同一脚本条目走 Generate 有、走
// Stream 恒为 nil，mock 与真实 provider（流式尾帧带 usage/finish）契约分叉。
func TestStreamResponseMeta(t *testing.T) {
	m := &Model{Script: []Resp{{Content: "半截", FinishReason: "length", Prompt: 10, Completion: 5}}}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := sr.Recv()
	if err != nil {
		t.Fatal(err)
	}
	meta := msg.ResponseMeta
	if meta == nil {
		t.Fatal("Stream 路径应按 Resp 填 ResponseMeta（llm#166）")
	}
	if meta.FinishReason != "length" {
		t.Fatalf("FinishReason = %q，应为 length", meta.FinishReason)
	}
	if meta.Usage == nil || meta.Usage.PromptTokens != 10 ||
		meta.Usage.CompletionTokens != 5 || meta.Usage.TotalTokens != 15 {
		t.Fatalf("Usage 应随 ResponseMeta 落下: %+v", meta.Usage)
	}
}

// TestGenerateUsageCompletionOnly 回归（oagentkit/llm#167A）：只给 Completion
// 的条目此前整份 ResponseMeta 都不生成（填充判据只看 Prompt > 0），连同
// FinishReason 一起丢——断言失败会被误导成「usage 记账错」。
func TestGenerateUsageCompletionOnly(t *testing.T) {
	m := &Model{RepeatLast: true, Script: []Resp{{Content: "x", Completion: 7, FinishReason: "stop"}}}
	out, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil {
		t.Fatal(err)
	}
	if out.ResponseMeta == nil {
		t.Fatal("Completion 非零也应生成 ResponseMeta（llm#167A）")
	}
	if out.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("FinishReason 不应被一并丢弃: %q", out.ResponseMeta.FinishReason)
	}
	u := out.ResponseMeta.Usage
	if u == nil || u.CompletionTokens != 7 || u.TotalTokens != 7 {
		t.Fatalf("Completion-only 应落 Usage: %+v", u)
	}
}

// TestStreamContentFallbackToScript 钉字段文档口径（oagentkit/llm#167C）：
// StreamContent 为空时回落到脚本条目（Generate/Stream 共享调用序），脚本
// 为空/耗尽才报错——文档此前写「空则报错」，实际脚本非空时不报错。
func TestStreamContentFallbackToScript(t *testing.T) {
	m := &Model{Script: []Resp{{Content: "from-script"}}}
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil {
		t.Fatalf("StreamContent 为空应回落到脚本条目而非报错（llm#167C）: %v", err)
	}
	msg, err := sr.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "from-script" {
		t.Fatalf("应回落脚本内容, got %q", msg.Content)
	}
	if _, err := m.Stream(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "脚本耗尽") {
		t.Fatalf("脚本耗尽才报错: %v", err)
	}
}

// TestExhaustCallNumberTracksRealCallOrder 回归（oagentkit/llm#167D）：耗尽
// 分支此前不自增 Calls，第 2/3/4 次耗尽报的都是同一个「第 2 次调用」。
func TestExhaustCallNumberTracksRealCallOrder(t *testing.T) {
	m := &Model{Script: []Resp{{Content: "only"}}}
	if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 3 {
		_, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")})
		if err == nil {
			t.Fatal("脚本耗尽应报错")
		}
		got = append(got, err.Error())
	}
	for i, want := range []string{"第 2 次调用", "第 3 次调用", "第 4 次调用"} {
		if !strings.Contains(got[i], want) {
			t.Errorf("第 %d 次耗尽错误应含 %q，实际 %q", i+2, want, got[i])
		}
	}
	// Calls 与错误编号同源：含耗尽报错那几次在内的真实调用数。
	if m.Calls != 4 {
		t.Errorf("Calls = %d，应为 4（含 3 次耗尽调用）", m.Calls)
	}
}

// TestToolModelWithToolsAppendsToolTag 回归（oagentkit/llm#167B）：ToolTag
// 此前只被原样复制给派生副本、从未拼进 Content（死字段）——派生前后响应
// 完全相同，「区分派生前后」这个用途不成立。
func TestToolModelWithToolsAppendsToolTag(t *testing.T) {
	base := &Model{RepeatLast: true, Script: []Resp{{Content: "answer"}}}
	tm := &ToolModel{Model: base, ToolTag: "T"}
	in := []*schema.Message{schema.UserMessage("q")}

	// 父桩自身不加后缀（后缀是「派生副本」的标记）。
	parent, err := tm.Generate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Content != "answer" {
		t.Fatalf("父桩不应追加 ToolTag: %q", parent.Content)
	}

	derived, err := tm.WithTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := derived.Generate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "answerT" {
		t.Fatalf("派生副本 Generate 应追加 ToolTag（llm#167B）: %q", out.Content)
	}

	sr, err := derived.Stream(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := sr.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "answerT" {
		t.Fatalf("派生副本 Stream 应追加 ToolTag（llm#167B）: %q", msg.Content)
	}
}
