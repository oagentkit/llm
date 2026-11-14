package llmtest

// 2026-11-14 issue 清零战回归（#196）：StreamContent 恒定内容流不消费脚本条目，
// 脚本序只由真正取脚本的调用推进——混合形态（Script + StreamContent 同置）下
// Generate 不再取到错位条目，「脚本写漏立即可见」的耗尽保护不失效。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestStreamContentDoesNotConsumeScript_Issue196(t *testing.T) {
	m := &Model{StreamContent: "stream", Script: []Resp{
		{Err: errors.New("first-fail")},
		{Content: "second-ok"},
	}}
	ctx := context.Background()
	in := []*schema.Message{schema.UserMessage("q")}

	// 两次恒定内容流：不取脚本条目。
	for range 2 {
		sr, err := m.Stream(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		msg, err := sr.Recv()
		if err != nil || msg.Content != "stream" {
			t.Fatalf("StreamContent 应恒定返回: %v %q", err, msg.Content)
		}
	}

	// Generate 按脚本序取条目——不错位（旧实现：Stream 捷径以 Calls 为脚本
	// 下标，序号被凭空推进两格，这里会直接「脚本耗尽」）。
	out1, err := m.Generate(ctx, in)
	if err == nil || out1 != nil {
		t.Fatalf("第一条脚本应返回错误: %v", out1)
	}
	if !strings.Contains(err.Error(), "first-fail") {
		t.Fatalf("应取到脚本第 1 条: %v", err)
	}
	out2, err := m.Generate(ctx, in)
	if err != nil || out2.Content != "second-ok" {
		t.Fatalf("应取到脚本第 2 条: %v %v", err, out2)
	}

	// Calls 为 Generate+Stream 总调用数（含错位防护下的真实序）。
	if m.Calls != 4 {
		t.Fatalf("Calls = %d，应为 4", m.Calls)
	}

	// 耗尽保护仍生效：第 3 次 Generate 报耗尽，编号是真实调用序。
	_, err = m.Generate(ctx, in)
	if err == nil || !strings.Contains(err.Error(), "脚本耗尽（第 5 次调用）") {
		t.Fatalf("耗尽保护应失效立刻可见: %v", err)
	}
}
