// 本文件族是 LLM Provider 抽象与多后端支持（Provider 接口 + FallbackChain
// 模型降级链）。包级总览见 client.go 的唯一 package doc。
package llm

import (
	"github.com/cloudwego/eino/components/model"
)

// Provider LLM 提供者接口：抽象模型创建与配置。
type Provider interface {
	// Name 提供者名称（如 "openai", "anthropic", "ollama"）。
	Name() string
	// Model 返回 eino ChatModel 实例。
	Model() model.BaseChatModel
	// ModelName 返回模型标识（如 "gpt-4o", "deepseek-v4"）。
	ModelName() string
	// ContextTokens 返回模型上下文窗口大小（token 数）。
	ContextTokens() int
	// MaxOutputTokens 返回单次生成输出上限。
	MaxOutputTokens() int
	// CostPer1KTokens 返回每 1000 token 的成本（美元）。
	// 用于成本追踪，返回 0 表示不计费。
	//
	// ⚠️ 返回 0 有歧义：「这个模型免费」与「没配价」分不开。llm 层的定价
	// 会把 0 视为「未指定」，回落到内建价表（PriceTable）；价表也查不到时
	// 记为**价格未知**（CostRecord.PriceStatus = PriceUnpriced），不是 0 成本。
	// 想显式声明单价（折扣价/加价率/人民币折算价）见 PricedProvider。
	CostPer1KTokens() (prompt, completion float64)
}

// PricedProvider 可选扩展接口：Provider **显式**给出单价。
//
// 这是「模型档案带 pricing」的口子：实现了它的 Provider，其单价**优先于**
// llm 内建价表；没实现时 llm 才会按模型名去价表里查。
//
// 单价单位是**美元 / 每 100 万 token**（Price），与 CostPer1KTokens 的
// 每 1K 不同——两套单位并存容易错账一个数量级，所以新实现方用这个接口。
type PricedProvider interface {
	Provider
	// ModelPricing 返回该模型的单价与是否已定价。
	// 返回 (Price{}, false) 表示「未定价」，llm 会继续回落到价表。
	ModelPricing() (Price, bool)
}

// FallbackChain 模型降级链：按优先级尝试多个 Provider。
type FallbackChain struct {
	Providers []Provider
}

// NewFallbackChain 创建降级链。
func NewFallbackChain(providers ...Provider) *FallbackChain {
	return &FallbackChain{Providers: providers}
}

// Primary 返回主 Provider（优先级最高）。
func (fc *FallbackChain) Primary() Provider {
	if len(fc.Providers) == 0 {
		return nil
	}
	return fc.Providers[0]
}

// Fallback 返回备选 Provider 列表（不含主 Provider）。
func (fc *FallbackChain) Fallback() []Provider {
	if len(fc.Providers) <= 1 {
		return nil
	}
	return fc.Providers[1:]
}
