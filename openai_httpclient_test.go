package llm

// OpenAIProviderConfig.HTTPClient 的注入点：宿主要挂传输层守卫（拨号层出站守卫）
// 时，唯一能插进去的位置就是这里。本文件钉两件事：
//  1. 四条路径的取舍（注入 × 捕获 Retry-After）；
//  2. 端到端——注入的 Client 真的承担了出站请求（不是只过了一遍 helper）。
//
// 端到端那一条是必须的：注入点装在 config 上，中间隔着 fillDefaults →
// httpClient → chatModelConfig → eino 的 NewChatModel 四跳。任何一跳把它丢了，
// 单测全绿而守卫根本没挂上——**守卫没挂上等于没挂**（宿主以为出站有保护，
// 实际裸奔），这是最贵的一种静默失效。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/oekit/pkg/httpjson"
)

// countingTransport 计数型 transport（测试用）。用**指针类型**而不是函数类型：
// 函数类型不可比较，断言「Base 就是宿主注入的那个 Transport」时会 panic。
type countingTransport struct {
	calls *atomic.Int32
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

func TestOpenAIProviderConfigHTTPClientPaths(t *testing.T) {
	calls := &atomic.Int32{}
	guardTransport := &countingTransport{calls: calls}
	injected := &http.Client{Timeout: 7 * time.Second, Transport: guardTransport}

	t.Run("不注入且不捕获→nil（由 eino 内部按 config.Timeout 构造）", func(t *testing.T) {
		c := OpenAIProviderConfig{Timeout: 30 * time.Second}
		if got := c.httpClient(); got != nil {
			t.Fatalf("应返回 nil，实际 %v", got)
		}
	})

	t.Run("不注入且捕获→自建 client（既有行为逐字不变）", func(t *testing.T) {
		c := OpenAIProviderConfig{Timeout: 30 * time.Second, CaptureRetryAfter: true}
		got := c.httpClient()
		if got == nil {
			t.Fatal("应自建 client")
		}
		if got.Timeout != 30*time.Second {
			t.Fatalf("Timeout = %v，应为 30s", got.Timeout)
		}
		rt, ok := got.Transport.(httpjson.RetryAfterTransport)
		if !ok {
			t.Fatalf("Transport 类型 = %T，应为 httpjson.RetryAfterTransport", got.Transport)
		}
		if rt.Base != http.DefaultTransport {
			t.Fatalf("Base = %v，应为 http.DefaultTransport", rt.Base)
		}
	})

	t.Run("注入且不捕获→原样使用宿主的 client", func(t *testing.T) {
		c := OpenAIProviderConfig{HTTPClient: injected, Timeout: 30 * time.Second}
		if got := c.httpClient(); got != injected {
			t.Fatal("应原样返回注入的 client")
		}
	})

	t.Run("注入且捕获→在宿主 Transport 外面套一层，且不动宿主的 client", func(t *testing.T) {
		c := OpenAIProviderConfig{HTTPClient: injected, Timeout: 30 * time.Second, CaptureRetryAfter: true}
		got := c.httpClient()
		if got == injected {
			t.Fatal("应派生一个新 client，而不是原样返回")
		}
		if got.Timeout != injected.Timeout {
			t.Fatalf("派生 client 的 Timeout = %v，应保留注入的 %v", got.Timeout, injected.Timeout)
		}
		rt, ok := got.Transport.(httpjson.RetryAfterTransport)
		if !ok {
			t.Fatalf("Transport 类型 = %T，应为 httpjson.RetryAfterTransport", got.Transport)
		}
		if rt.Base != injected.Transport {
			t.Fatalf("Base 应是宿主注入的 Transport，实际 %v", rt.Base)
		}
		// 宿主的 client 不能被就地改写——宿主可能还在用它。
		if _, ok := injected.Transport.(httpjson.RetryAfterTransport); ok {
			t.Fatal("不该改写宿主 client 的 Transport")
		}
	})
}

// TestOpenAIProviderInjectedClientCarriesRequest 端到端：注入的 Client 真的
// 承担了出站请求（守卫挂在它的 Transport 上才有意义）。
func TestOpenAIProviderInjectedClientCarriesRequest(t *testing.T) {
	for _, tc := range []struct {
		name              string
		captureRetryAfter bool
	}{
		{"注入且不捕获", false},
		{"注入且捕获 Retry-After", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m",` +
					`"choices":[{"index":0,"message":{"role":"assistant","content":"注入生效"},"finish_reason":"stop"}],` +
					`"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
			}))
			defer srv.Close()

			injected := &http.Client{Transport: &countingTransport{calls: &calls}}

			p, err := NewOpenAIProvider(context.Background(), OpenAIProviderConfig{
				BaseURL: srv.URL, APIKey: "k", Model: "m",
				HTTPClient:        injected,
				CaptureRetryAfter: tc.captureRetryAfter,
			})
			if err != nil {
				t.Fatal(err)
			}

			out, err := p.Model().Generate(context.Background(), []*schema.Message{schema.UserMessage("q")})
			if err != nil {
				t.Fatalf("生成失败: %v", err)
			}
			if out.Content != "注入生效" {
				t.Fatalf("内容异常: %q", out.Content)
			}
			if calls.Load() == 0 {
				t.Fatal("注入的 Client 没被使用——出站请求没走宿主的 Transport")
			}
		})
	}
}
