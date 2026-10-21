package llm

import (
	"context"
	"github.com/oekit/pkg/httpjson"
	"net/http"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// OpenAIProvider OpenAI 兼容 LLM 提供者。
// 支持 OpenAI / 百炼 / DeepSeek / 任何 OpenAI 兼容端点。
type OpenAIProvider struct {
	modelName       string
	contextTokens   int
	maxOutputTokens int
	timeout         time.Duration
	costPer1K       [2]float64 // [prompt, completion]

	chatModel model.BaseChatModel
}

// OpenAIProviderConfig OpenAI 提供者配置。
type OpenAIProviderConfig struct {
	BaseURL         string
	APIKey          string
	Model           string
	ContextTokens   int           // 模型上下文窗口（默认 128k）
	MaxOutputTokens int           // 单次输出上限（默认从窗口派生）
	Timeout         time.Duration // 请求超时（默认 60s）
	// CaptureRetryAfter 捕获 429 响应的 Retry-After 头（批次四十五，对标 ZCode
	// runner-retry 的服务端退避建议）：经 ctx sink 透出给重试环的退避取舍。
	// 需要 http.Client 注入（Timeout 语义由注入的 Client 自带保留）。
	CaptureRetryAfter bool
	// HTTPClient 注入自定义 HTTP 客户端（**可选**，nil = 走缺省）。
	//
	// 为什么需要：出站守卫要真正闭合「写面校验 → 拨号」的 DNS rebinding 窗口，
	// 守卫必须挂在 Transport.DialContext 上——写面校验（按 URL 判）拦不住
	// 「解析与拨号之间权威 DNS 改口」。而 LLM 的 base_url 是**用户可配**的
	// （平台代发 LLM 请求的目标），是这条链上暴露最大的一个面。
	//
	// 语义：**显式注入优先于内部构造**。注入后由注入的 Client 完全接管超时
	// （Timeout 语义由注入的 Client 自带保留；eino 侧同一口径——设了 HTTPClient
	// 就不再用 config.Timeout），本仓不再叠加自己的 Timeout。
	// 注入的 Client 的 Transport 为 nil 时由 net/http 回落 http.DefaultTransport。
	//
	// 与 CaptureRetryAfter 可叠加：注入的同时要捕获 Retry-After 时，本仓在
	// **注入的 Transport 外面**套一层 RetryAfterTransport——守卫与退避建议
	// 捕获不再互相排斥（此前二者是排斥关系：自建 client 走 http.DefaultTransport，
	// 宿主的守卫挂不上去）。
	//
	// 宿主侧参考（agent-harness 的 netguard）：
	//
	//	hc, err := netguard.NewClient(nil, netguard.Allow{Private: true, Loopback: true})
	//	cfg.HTTPClient = hc   // hc.Transport 挂了 netguard.DialFunc
	HTTPClient          *http.Client
	CostPer1KPrompt     float64 // 每 1000 prompt token 成本（美元）
	CostPer1KCompletion float64 // 每 1000 completion token 成本（美元）
}

// NewOpenAIProvider 创建 OpenAI 兼容提供者。
func NewOpenAIProvider(ctx context.Context, cfg OpenAIProviderConfig) (*OpenAIProvider, error) {
	cfg.fillDefaults()
	httpClient := cfg.httpClient()
	chatModel, err := openai.NewChatModel(ctx, cfg.chatModelConfig(httpClient))
	if err != nil {
		return nil, err
	}

	return &OpenAIProvider{
		modelName:       cfg.Model,
		contextTokens:   cfg.ContextTokens,
		maxOutputTokens: cfg.MaxOutputTokens,
		timeout:         cfg.Timeout,
		costPer1K:       [2]float64{cfg.CostPer1KPrompt, cfg.CostPer1KCompletion},
		chatModel:       chatModel,
	}, nil
}

// fillDefaults 配置缺省落位：窗口 128k、输出 5%（下限 4096）、超时 60s。
// MaxOutputTokens 0 = 从窗口派生缺省；<0 = 不下发 max_tokens 参数（部分推理
// 模型要求 max_completion_tokens 或拒绝该参数）。
func (c *OpenAIProviderConfig) fillDefaults() {
	if c.ContextTokens <= 0 {
		c.ContextTokens = 128_000
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = c.ContextTokens * 5 / 100
		if c.MaxOutputTokens < 4096 {
			c.MaxOutputTokens = 4096
		}
	}
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
}

// httpClient 决定注入给下游的 http.Client。
//
// 三条路径：
//   - **宿主注入了 HTTPClient**：以其为准（Timeout 语义由注入的 Client 自带保留）。
//     若同时要 CaptureRetryAfter，则在注入的 Transport 外面套一层
//     RetryAfterTransport——两条路径合流，守卫与 Retry-After 捕获不再互相排斥。
//     派生时**复制**一份 Client，不改动宿主传进来的那个（宿主可能还在用它）。
//   - 宿主没注入且要捕获 Retry-After：自建 client（既有行为，逐字不变）。
//   - 宿主没注入且不捕获：返回 nil——走 openai 包内部缺省（既有行为）。
func (c *OpenAIProviderConfig) httpClient() *http.Client {
	if c.HTTPClient == nil {
		if !c.CaptureRetryAfter {
			return nil
		}
		return &http.Client{
			Timeout:   c.Timeout,
			Transport: httpjson.RetryAfterTransport{Base: http.DefaultTransport},
		}
	}
	if !c.CaptureRetryAfter {
		return c.HTTPClient
	}
	derived := *c.HTTPClient
	derived.Transport = httpjson.RetryAfterTransport{Base: c.HTTPClient.Transport}
	return &derived
}

// chatModelConfig 组装 eino openai.ChatModelConfig（maxTokens 指针化）。
func (c *OpenAIProviderConfig) chatModelConfig(httpClient *http.Client) *openai.ChatModelConfig {
	var maxTokens *int
	if c.MaxOutputTokens > 0 {
		t := c.MaxOutputTokens
		maxTokens = &t
	}
	return &openai.ChatModelConfig{
		BaseURL:    c.BaseURL,
		APIKey:     c.APIKey,
		Model:      c.Model,
		MaxTokens:  maxTokens,
		Timeout:    c.Timeout,
		HTTPClient: httpClient,
	}
}

func (p *OpenAIProvider) Name() string               { return "openai" }
func (p *OpenAIProvider) Model() model.BaseChatModel { return p.chatModel }
func (p *OpenAIProvider) ModelName() string          { return p.modelName }
func (p *OpenAIProvider) ContextTokens() int         { return p.contextTokens }
func (p *OpenAIProvider) MaxOutputTokens() int       { return p.maxOutputTokens }
func (p *OpenAIProvider) CostPer1KTokens() (float64, float64) {
	return p.costPer1K[0], p.costPer1K[1]
}

// AttemptTimeout 单次尝试超时（Resilient 降级链按 Provider 独立限时）。
func (p *OpenAIProvider) AttemptTimeout() time.Duration { return p.timeout }
