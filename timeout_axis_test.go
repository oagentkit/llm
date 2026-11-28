package llm

// 超时三轴身份契约·模型轴（oagentkit/guardx#222；与 guardx/toolfail
// timeout_axis_test.go 对偶，两仓 CI 各自常绿——依赖方向决定两仓不能互
// import，契约以「同一判据面两处各钉一半」的形式成立，对照表见
// matrix-tools docs/exec-semantics.md 超时三轴节）。
//
// 钉的是「模型超时 ≠ 用户取消」在 errors.Is / IsTransientStreamError 维度的
// 区分，以及**同一字面量不同值**的事故形态回归（kernel #170/#333/#355 教训）。

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestModelTimeoutIsTransient_Issue222 轴①模型超时：attempt ctx 到点 =
// DeadlineExceeded，且按瞬态处理（恢复循环重开流的判据面）。
func TestModelTimeoutIsTransient_Issue222(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	err := ctx.Err() // context.DeadlineExceeded

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("模型超时必须保留 DeadlineExceeded 身份")
	}
	// 模型超时 = 瞬态：挂死流被兜底限时打断，自动恢复语义（recovery.go 判据）。
	if !IsTransientStreamError(err) {
		t.Fatal("模型超时必须判瞬态（IsTransientStreamError=true，恢复循环重开流）")
	}
}

// TestUserCancelNotModelTimeout_Issue222 对照轴（用户取消）：取消既不是
// DeadlineExceeded、也不进瞬态恢复——「模型超时 ≠ 用户取消」的 llm 侧半边。
func TestUserCancelNotModelTimeout_Issue222(t *testing.T) {
	cause := errors.New("用户点停止")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	err := ctx.Err() // context.Canceled

	if !errors.Is(err, context.Canceled) {
		t.Fatal("取消错误必须保留 Canceled 身份")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("用户取消不得被认成模型超时——两者 errors.Is 维度必须分开")
	}
	// 取消不进瞬态恢复：对已取消的流空转恢复轮是纯浪费（recovery.go 显式排除）。
	if IsTransientStreamError(err) {
		t.Fatal("用户取消不得判瞬态（恢复循环会对已取消的流空转）")
	}
	// 身份在 cause **值**上：errors.Is 解到取消时传入的那个值（文本可复制，值不可）。
	if !errors.Is(context.Cause(ctx), cause) {
		t.Fatal("取消 cause 必须可解（身份走 context.Cause 的值，不走文本）")
	}
}

// TestSameTextDifferentValue_Issue222 同一字面量不同值回归：把错误链按文本
// 复制一份（不共享值），errors.Is 不许认领——值身份不是文本身份。
//
// 注：IsTransientStreamError 对**已登记的瞬态文案**（transientErrorReasons
// 子串白名单，含 "deadline exceeded"）有意放行——那是文案形态的显式登记，
// 不是文本身份的泄露。两维分开：**认领（errors.Is）靠值，瞬态判定靠白名单**。
func TestSameTextDifferentValue_Issue222(t *testing.T) {
	wrapped := fmt.Errorf("model call: %w", context.DeadlineExceeded)
	textCopy := errors.New(wrapped.Error()) // 文本逐字相同、值不同

	if !errors.Is(wrapped, context.DeadlineExceeded) {
		t.Fatal("真链必须解到底层超时（正对照）")
	}
	if errors.Is(textCopy, context.DeadlineExceeded) {
		t.Fatal("同字面量不同值必须不被 errors.Is 认领（事故形态回归）")
	}
}
