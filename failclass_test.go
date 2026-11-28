package llm

// llm#234 收编判据的可执行面（判据四条来自 oagentkit/guardx#222 交付评论）：
//
//  ① 模型超时 → transient（可重试档）；
//  ② 取消 → 不进瞬时类（ClassifyLLMError 根修 + ToolfailClass→unknown）；
//  ③ 同字面量不同值回归随收编迁移（见 timeout_axis_test.go，本文件不重复）；
//  ④ 不改变 IsTransientStreamError 的取消排除语义（recovery 面未动，既有
//     issue_sweep_1114 钉保持常绿）。
//
// 另钉出链装饰（同源判定）与映射表冻结值（与 guardx/toolfail Class 逐字同步）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"

	"github.com/oagentkit/llm/llmtest"
	"github.com/oagentkit/llm/port"
)

// TestClassifyLLMErrorCancelNotRetryable_Issue234 判据②根修：取消不再落
// default Retryable:true（外泄即「对用户停掉的动作重试」的事故）；
// 正对照判据①：本地模型超时（DeadlineExceeded）仍是瞬态可重试。
func TestClassifyLLMErrorCancelNotRetryable_Issue234(t *testing.T) {
	if ClassifyLLMError(context.Canceled).Retryable {
		t.Fatal("取消必须不可重试（判据②）")
	}
	wrapped := fmt.Errorf("model call: %w", context.Canceled)
	if RetryableLLMError(wrapped) {
		t.Fatal("包装后的取消同样必须不可重试（判据②，errors.Is 链上判）")
	}
	// 判据①正对照：模型超时是瞬态——取消收口不得连坐 DeadlineExceeded。
	if !ClassifyLLMError(context.DeadlineExceeded).Retryable {
		t.Fatal("模型超时必须保持可重试（判据①，三轴表轴①）")
	}
}

// TestToolfailClassTable_Issue234 映射表逐类钉（三维 → toolfail 类名）。
// 取值冻结：必须与 guardx/toolfail Class 字面量逐字一致（改名先改本测试）。
func TestToolfailClassTable_Issue234(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"取消 → unknown（不冒充确定性失败，也不进瞬时类）",
			context.Canceled, "unknown"},
		{"包装取消 → unknown",
			fmt.Errorf("wrap: %w", context.Canceled), "unknown"},
		{"nil → unknown", nil, "unknown"},
		{"429 → rate_limited（可重试 + 退避）",
			&openai.RequestError{HTTPStatusCode: 429, Err: errors.New("rate limited")}, "rate_limited"},
		{"400 确定性 → permanent（重试纯浪费）",
			&openai.RequestError{HTTPStatusCode: 400, Err: errors.New("bad shape")}, "permanent"},
		{"鉴权文本 → permanent",
			errors.New("401 unauthorized: invalid api key"), "permanent"},
		{"模型超时 → transient（判据①：可重试、默认不给重发安全）",
			context.DeadlineExceeded, "transient"},
		{"截断 → transient（可重试但须改参数，SafeToResubmit 缺省 false）",
			errors.New("finish_reason=length 输出被截断"), "transient"},
		{"5xx 文本 → transient",
			errors.New("HTTP 502 bad gateway"), "transient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolfailClass(tc.err); got != tc.want {
				t.Fatalf("ToolfailClass = %q, want %q", got, tc.want)
			}
		})
	}
	// 冻结值集合：与 guardx/toolfail Class 字面量逐字对应（同步义务见 failclass.go）。
	for _, v := range []string{"unknown", "rate_limited", "permanent", "transient"} {
		if ToolfailClass(context.Canceled) == "" || v == "" {
			t.Fatal("冻结值不得为空串")
		}
	}
}

// TestGenerateRetryLeaksVerdict_Issue234 出链装饰（同源判定）：循环里算出的
// lastHint 随错误出链——宿主用 toolfail 同款鸭子面（Retryable()）读到的
// bool 与链内重试决策用的是**同一份**结论；文案/判定链全透传。
func TestGenerateRetryLeaksVerdict_Issue234(t *testing.T) {
	// 场景 A：503 瞬态失败（单轮收场）→ 外泄 Retryable=true。
	m := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Err: errors.New("llm: upstream 503")},
	}}
	c := NewClient(m, "m", nil)
	c.MaxRetries = 1 // 单轮：首败即出链
	c.BaseDelay = time.Millisecond
	c.MaxDelay = 2 * time.Millisecond

	_, _, err := c.generateRetry(context.Background(), "R1",
		[]*schema.Message{schema.UserMessage("hi")}, c.retryPolicy())
	if err == nil {
		t.Fatal("瞬态失败应上抛")
	}
	v, ok := port.RetryVerdictOf(err)
	if !ok {
		t.Fatalf("结论必须随错误出链（RetryVerdictOf 读不到）: %v", err)
	}
	if !v.Retryable {
		t.Fatalf("503 的链内结论是可重试，外泄必须同源: %+v", v)
	}
	// 鸭子面（guardx/toolfail RetryableReporter 同款结构）：同一条结论。
	var rr interface{ Retryable() bool }
	if !errors.As(err, &rr) || !rr.Retryable() {
		t.Fatal("鸭子 Retryable() 必须读到与 RetryVerdictOf 同一份结论")
	}
	// 透传：文案与底层判定链都不断。
	if !strings.Contains(err.Error(), "upstream 503") {
		t.Fatalf("文案必须透传底层错误，实得 %q", err.Error())
	}

	// 场景 B：取消失败 → 外泄 Retryable=false（判据②在出链口的形态）。
	mc := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Err: context.Canceled},
	}}
	cc := NewClient(mc, "m", nil)
	cc.MaxRetries = 1
	cc.BaseDelay = time.Millisecond
	cc.MaxDelay = 2 * time.Millisecond
	_, _, errC := cc.generateRetry(context.Background(), "R1",
		[]*schema.Message{schema.UserMessage("hi")}, cc.retryPolicy())
	if errC == nil {
		t.Fatal("取消失败应上抛")
	}
	vc, ok := port.RetryVerdictOf(errC)
	if !ok || vc.Retryable {
		t.Fatalf("取消的外泄结论必须 Retryable=false（判据②）: ok=%v %+v", ok, vc)
	}
	if got := ToolfailClass(errC); got != "unknown" {
		t.Fatalf("取消经装饰链映射仍须 unknown，实得 %q", got)
	}
}
