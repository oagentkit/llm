package llm

// #210 回归：结构化 HTTP 状态码判定不能只认 go-openai RequestError。
// 非 OpenAI provider（anthropic 兼容层/自建网关/eino-ext/宿主 client）的
// 错误只要实现 HTTPStatusCode() int（或 StatusCode() int），就必须走结构化
// 判定；openai RequestError 特例与无状态码的文本兜底保持原行为。

import (
	"errors"
	"fmt"
	"testing"

	openai "github.com/meguminnnnnnnnn/go-openai"
)

// httpStatusCoderErr 自定义 provider 错误：实现结构化契约，错误文案不含任何
// 限流/鉴权 marker——命中只可能来自状态码通道。
type httpStatusCoderErr struct {
	code int
}

func (e *httpStatusCoderErr) Error() string       { return "provider call failed" }
func (e *httpStatusCoderErr) HTTPStatusCode() int { return e.code }

// httpStatusValueErr 值接收者形态（#210 验收原形：type httpErr struct{ code int }
// + 值方法），验证接口匹配不依赖指针接收者。
type httpStatusValueErr struct {
	code int
}

func (e httpStatusValueErr) Error() string       { return "provider call failed" }
func (e httpStatusValueErr) HTTPStatusCode() int { return e.code }

// statusCodeErr 只实现 StatusCode() int 形态（部分 SDK 的命名）。
type statusCodeErr struct {
	code int
}

func (e *statusCodeErr) Error() string   { return "provider call failed" }
func (e *statusCodeErr) StatusCode() int { return e.code }

// TestClassifyLLMErrorStatusCoder_Issue210 通用状态码接口优先：fmt 包装后
// 429 判限流可重试、401 判确定性失败；HTTPStatusCode 与 StatusCode 两种
// 命名都认。
func TestClassifyLLMErrorStatusCoder_Issue210(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
		rateLimit bool
	}{
		{
			name:      "HTTPStatusCode 429 fmt 包装",
			err:       fmt.Errorf("provider call: %w", &httpStatusCoderErr{code: 429}),
			retryable: true,
			rateLimit: true,
		},
		{
			name:      "HTTPStatusCode 401 fmt 包装",
			err:       fmt.Errorf("provider call: %w", &httpStatusCoderErr{code: 401}),
			retryable: false,
			rateLimit: false,
		},
		{
			name:      "HTTPStatusCode 408 仍可重试",
			err:       &httpStatusCoderErr{code: 408},
			retryable: true,
			rateLimit: false,
		},
		{
			name:      "HTTPStatusCode 400 确定性失败",
			err:       &httpStatusCoderErr{code: 400},
			retryable: false,
			rateLimit: false,
		},
		{
			name:      "HTTPStatusCode 值接收者 429 fmt 包装",
			err:       fmt.Errorf("provider call: %w", httpStatusValueErr{code: 429}),
			retryable: true,
			rateLimit: true,
		},
		{
			name:      "HTTPStatusCode 值接收者 401 fmt 包装",
			err:       fmt.Errorf("provider call: %w", httpStatusValueErr{code: 401}),
			retryable: false,
			rateLimit: false,
		},
		{
			name:      "StatusCode 429 fmt 包装",
			err:       fmt.Errorf("gateway: %w", &statusCodeErr{code: 429}),
			retryable: true,
			rateLimit: true,
		},
		{
			name:      "StatusCode 401 fmt 包装",
			err:       fmt.Errorf("gateway: %w", &statusCodeErr{code: 401}),
			retryable: false,
			rateLimit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint := ClassifyLLMError(tc.err)
			if hint.Retryable != tc.retryable {
				t.Errorf("Retryable = %v, want %v", hint.Retryable, tc.retryable)
			}
			if hint.IsRateLimit != tc.rateLimit {
				t.Errorf("IsRateLimit = %v, want %v", hint.IsRateLimit, tc.rateLimit)
			}
			if got := IsRateLimitError(tc.err); got != tc.rateLimit {
				t.Errorf("IsRateLimitError = %v, want %v", got, tc.rateLimit)
			}
		})
	}
}

// TestClassifyLLMErrorOpenAIRequestErrorUnchanged_Issue210 既有 go-openai
// 特例路径行为不变（含 fmt 包装穿透）。
func TestClassifyLLMErrorOpenAIRequestErrorUnchanged_Issue210(t *testing.T) {
	wrapped429 := fmt.Errorf("provider: %w", &openai.RequestError{
		HTTPStatusCode: 429,
		Err:            errors.New("rate limited"),
	})
	if !IsRateLimitError(wrapped429) || !RetryableLLMError(wrapped429) {
		t.Fatalf("openai RequestError 429 应判限流可重试: %+v", ClassifyLLMError(wrapped429))
	}
	wrapped401 := fmt.Errorf("provider: %w", &openai.RequestError{
		HTTPStatusCode: 401,
		Err:            errors.New("unauthorized"),
	})
	if IsRateLimitError(wrapped401) || RetryableLLMError(wrapped401) {
		t.Fatalf("openai RequestError 401 应判确定性失败: %+v", ClassifyLLMError(wrapped401))
	}
}

// TestClassifyLLMErrorTextFallbackUnchanged_Issue210 无结构化状态码仍走文本
// 兜底：既有 #64/#78/#114/#125 黄金口径不回归。
func TestClassifyLLMErrorTextFallbackUnchanged_Issue210(t *testing.T) {
	if !IsRateLimitError(errors.New("429 rate limit")) {
		t.Fatal("无状态码的 429 文本应仍判限流")
	}
	if RetryableLLMError(errors.New("context length exceeded")) {
		t.Fatal("无状态码的上下文超限文本应仍判确定性失败")
	}
	if IsRateLimitError(errors.New("failed after 429ms")) {
		t.Fatal("耗时文案 429ms 不得判限流（llm#78 黄金口径）")
	}
	if !RetryableLLMError(errors.New("connection reset by peer")) {
		t.Fatal("无状态码网络错误应仍默认可重试")
	}
}
