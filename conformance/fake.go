package conformance

import (
	"context"
	"fmt"

	"github.com/oagentkit/llm/port"
)

// fakeCompleter 全合规 Completer（正对照）：尊重 ctx 取消，错误原样透传。
type fakeCompleter struct {
	output string
	err    error
}

func (f *fakeCompleter) Complete(ctx context.Context, req port.Request) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if f.err != nil {
		return "", f.err
	}
	return f.output, nil
}

// badCompleter 故意违规 Completer（负对照）：用 %v 包装错误。
//
// %v 不保留错误链，errors.Is / errors.As 无法穿透——ctx.Canceled 会被默认
// Classifier 误判为可重试，StatusError 分类载体丢失。conformance 套件
// 必须能对这种实现判红（证明套件有牙齿）。
type badCompleter struct {
	output string
	err    error
}

func (b *badCompleter) Complete(ctx context.Context, req port.Request) (string, error) {
	if b.err != nil {
		return "", fmt.Errorf("wrapped: %v", b.err) //nolint:err113 // 故意违规：%v 打断错误链是本负对照的测试点
	}
	return b.output, nil
}

// NewFakeFactory 返回全合规 Factory（正对照）。
//
// 跑 conformance.Run(t, NewFakeFactory()) 必须全绿。
func NewFakeFactory() Factory {
	return func(b Behavior) port.Completer {
		return &fakeCompleter{output: b.Output, err: b.Err}
	}
}

// NewBadFactory 返回故意违规 Factory（负对照）。
//
// 跑 conformance.Run(t, NewBadFactory()) 必须有子测试判红——
// 这就是套件的牙齿：违规实现逃不过契约检查。
func NewBadFactory() Factory {
	return func(b Behavior) port.Completer {
		return &badCompleter{output: b.Output, err: b.Err}
	}
}
