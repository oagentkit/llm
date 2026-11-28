package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/oagentkit/llm/port"
)

// TestConformance 正对照：全合规 fake 跑完整套件，必须全绿。
func TestConformance(t *testing.T) {
	Run(t, NewFakeFactory())
}

// TestNegativeControl 负对照：故意违规 fake 会被套件判红——证明套件有牙齿。
//
// 验证方式：直接比较合规 / 违规两个 fake 在同一分类场景下的结论差异。
// 违规 fake 以 %v 包装错误，errors.Is 穿不透，context.Canceled 被误判为
// 可重试——这正是 conformance.Run 的 classifier_ctx_cancel 子测试要判红的
// 那条语义。合规 fake 在同一场景下结论正确（不可重试）。
func TestNegativeControl(t *testing.T) {
	// 违规 fake：ctx.Canceled 被误判为可重试（套件应判红的场景）
	badFactory := NewBadFactory()
	badC := badFactory(Behavior{Err: context.Canceled})
	badChain := port.NewChain(port.WithCompleter("test", badC))

	_, badErr := badChain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if badErr == nil {
		t.Fatal("违规 fake 应返回错误")
	}
	badVerdict, ok := port.RetryVerdictOf(badErr)
	if !ok {
		t.Fatal("出链错误应携带 RetryVerdict")
	}
	if !badVerdict.Retryable {
		t.Fatal("违规 fake 应导致误分类（ctx.Canceled 被判可重试）——这是套件要抓的违规点")
	}

	// 合规 fake：ctx.Canceled 正确判不可重试（套件全绿的场景）
	goodFactory := NewFakeFactory()
	goodC := goodFactory(Behavior{Err: context.Canceled})
	goodChain := port.NewChain(port.WithCompleter("test", goodC))

	_, goodErr := goodChain.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if goodErr == nil {
		t.Fatal("合规 fake 应返回错误")
	}
	goodVerdict, ok := port.RetryVerdictOf(goodErr)
	if !ok {
		t.Fatal("出链错误应携带 RetryVerdict")
	}
	if goodVerdict.Retryable {
		t.Fatal("合规 fake 应正确分类 ctx.Canceled 为不可重试")
	}
}

// TestBadFakeBreaksErrorChain 负对照根因：违规 fake 以 %v 包装错误，
// errors.Is 无法穿透——分类载体（StatusError）同样丢失。
func TestBadFakeBreaksErrorChain(t *testing.T) {
	factory := NewBadFactory()

	// %v 包装后 errors.Is 穿不透 context.Canceled
	c := factory(Behavior{Err: context.Canceled})
	_, err := c.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if err == nil {
		t.Fatal("违规 fake 应返回错误")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("违规 fake 不应正确包装 context.Canceled——应以 fmt 百分号 v 打断错误链")
	}

	// %v 包装后 errors.As 穿不透 StatusError
	c2 := factory(Behavior{Err: &port.StatusError{Status: 429}})
	_, err2 := c2.Complete(context.Background(), port.Request{Prompt: "conformance"})
	if err2 == nil {
		t.Fatal("违规 fake 应返回错误")
	}
	var se *port.StatusError
	if errors.As(err2, &se) {
		t.Fatal("违规 fake 不应保留 StatusError 分类载体——应以 fmt 百分号 v 打断错误链")
	}
}
