// 本文件是根包（github.com/oagentkit/llm）导出符号的**镜像**。
//
// 内容必须与根包一致，一致性由 compat_test.go 的 TestCompatParity 强制：
// 根包增删了导出符号而本文件没跟上时，那条测试会红。
// ⇒ 看到它红，改的是**本文件**，不是那条测试。

package llm

import root "github.com/oagentkit/llm"

// 类型别名：与根包是**同一个类型**（type alias，不是新定义）。
type (
	// Attempt 是根包 Attempt 的类型别名（兼容层）。
	Attempt = root.Attempt
	// AttemptError 是根包 AttemptError 的类型别名（兼容层）。
	AttemptError = root.AttemptError
	// Budget 是根包 Budget 的类型别名（兼容层）。
	Budget = root.Budget
	// BudgetConfig 是根包 BudgetConfig 的类型别名（兼容层）。
	BudgetConfig = root.BudgetConfig
	// BudgetHolder 是根包 BudgetHolder 的类型别名（兼容层）。
	BudgetHolder = root.BudgetHolder
	// BudgetInjector 是根包 BudgetInjector 的类型别名（兼容层）。
	BudgetInjector = root.BudgetInjector
	// ChainLink 是根包 ChainLink 的类型别名（兼容层）。
	ChainLink = root.ChainLink
	// Client 是根包 Client 的类型别名（兼容层）。
	Client = root.Client
	// CostRecord 是根包 CostRecord 的类型别名（兼容层）。
	CostRecord = root.CostRecord
	// CostTracker 是根包 CostTracker 的类型别名（兼容层）。
	CostTracker = root.CostTracker
	// FailoverModel 是根包 FailoverModel 的类型别名（兼容层）。
	FailoverModel = root.FailoverModel
	// FallbackChain 是根包 FallbackChain 的类型别名（兼容层）。
	FallbackChain = root.FallbackChain
	// Generator 是根包 Generator 的类型别名（兼容层）。
	Generator = root.Generator
	// MatchKind 是根包 MatchKind 的类型别名（兼容层）。
	MatchKind = root.MatchKind
	// ModelSummary 是根包 ModelSummary 的类型别名（兼容层）。
	ModelSummary = root.ModelSummary
	// OpenAIProvider 是根包 OpenAIProvider 的类型别名（兼容层）。
	OpenAIProvider = root.OpenAIProvider
	// OpenAIProviderConfig 是根包 OpenAIProviderConfig 的类型别名（兼容层）。
	OpenAIProviderConfig = root.OpenAIProviderConfig
	// Price 是根包 Price 的类型别名（兼容层）。
	Price = root.Price
	// PriceLookup 是根包 PriceLookup 的类型别名（兼容层）。
	PriceLookup = root.PriceLookup
	// PriceStatus 是根包 PriceStatus 的类型别名（兼容层）。
	PriceStatus = root.PriceStatus
	// PriceTable 是根包 PriceTable 的类型别名（兼容层）。
	PriceTable = root.PriceTable
	// PricedProvider 是根包 PricedProvider 的类型别名（兼容层）。
	PricedProvider = root.PricedProvider
	// Provider 是根包 Provider 的类型别名（兼容层）。
	Provider = root.Provider
	// Resilient 是根包 Resilient 的类型别名（兼容层）。
	Resilient = root.Resilient
	// ResilientConfig 是根包 ResilientConfig 的类型别名（兼容层）。
	ResilientConfig = root.ResilientConfig
	// RetryHint 是根包 RetryHint 的类型别名（兼容层）。
	RetryHint = root.RetryHint
	// Sink 是根包 Sink 的类型别名（兼容层）。
	Sink = root.Sink
	// StageRouter 是根包 StageRouter 的类型别名（兼容层）。
	StageRouter = root.StageRouter
	// TokenAccountant 是根包 TokenAccountant 的类型别名（兼容层）。
	TokenAccountant = root.TokenAccountant
	// UsageCallCounter 是根包 UsageCallCounter 的类型别名（兼容层）。
	UsageCallCounter = root.UsageCallCounter
	// UsageRecord 是根包 UsageRecord 的类型别名（兼容层）。
	UsageRecord = root.UsageRecord
)

// 常量：原样转发，类型与值均与根包一致。
const (
	// MatchExact 是根包 MatchExact 的常量转发（兼容层）。
	MatchExact = root.MatchExact
	// MatchNone 是根包 MatchNone 的常量转发（兼容层）。
	MatchNone = root.MatchNone
	// MatchNormalized 是根包 MatchNormalized 的常量转发（兼容层）。
	MatchNormalized = root.MatchNormalized
	// MaxServerRetryAfter 是根包 MaxServerRetryAfter 的常量转发（兼容层）。
	MaxServerRetryAfter = root.MaxServerRetryAfter
	// MaxStreamRecoveryRetries 是根包 MaxStreamRecoveryRetries 的常量转发（兼容层）。
	MaxStreamRecoveryRetries = root.MaxStreamRecoveryRetries
	// PricePriced 是根包 PricePriced 的常量转发（兼容层）。
	PricePriced = root.PricePriced
	// PriceUnpriced 是根包 PriceUnpriced 的常量转发（兼容层）。
	PriceUnpriced = root.PriceUnpriced
)

// 函数转发：以变量形式转发根包的包级函数。
//
// 为什么是 var 而不是 func 包装：包装要**逐个复制签名**，
// 根包改签名时兼容层不会编译错误、而是静默变成另一个签名；
// var 转发把「签名」交给编译器守着，根包改了这里跟着改。
var (
	// BusyAdmissionDelay 是根包 BusyAdmissionDelay 的转发（兼容层）。
	BusyAdmissionDelay = root.BusyAdmissionDelay
	// ClassifyLLMError 是根包 ClassifyLLMError 的转发（兼容层）。
	ClassifyLLMError = root.ClassifyLLMError
	// DefaultPriceTable 是根包 DefaultPriceTable 的转发（兼容层）。
	DefaultPriceTable = root.DefaultPriceTable
	// IsContextExceededError 是根包 IsContextExceededError 的转发（兼容层）。
	IsContextExceededError = root.IsContextExceededError
	// IsContextExceededFinish 是根包 IsContextExceededFinish 的转发（兼容层）。
	IsContextExceededFinish = root.IsContextExceededFinish
	// IsProviderBusyCode 是根包 IsProviderBusyCode 的转发（兼容层）。
	IsProviderBusyCode = root.IsProviderBusyCode
	// IsRateLimitError 是根包 IsRateLimitError 的转发（兼容层）。
	IsRateLimitError = root.IsRateLimitError
	// IsTransientStreamError 是根包 IsTransientStreamError 的转发（兼容层）。
	IsTransientStreamError = root.IsTransientStreamError
	// IsTruncatedError 是根包 IsTruncatedError 的转发（兼容层）。
	IsTruncatedError = root.IsTruncatedError
	// LookupPrice 是根包 LookupPrice 的转发（兼容层）。
	LookupPrice = root.LookupPrice
	// LookupPriceStrict 是根包 LookupPriceStrict 的转发（兼容层）。
	LookupPriceStrict = root.LookupPriceStrict
	// NewBudget 是根包 NewBudget 的转发（兼容层）。
	NewBudget = root.NewBudget
	// NewClient 是根包 NewClient 的转发（兼容层）。
	NewClient = root.NewClient
	// NewCostTracker 是根包 NewCostTracker 的转发（兼容层）。
	NewCostTracker = root.NewCostTracker
	// NewCostTrackerWithTable 是根包 NewCostTrackerWithTable 的转发（兼容层）。
	NewCostTrackerWithTable = root.NewCostTrackerWithTable
	// NewFailoverModel 是根包 NewFailoverModel 的转发（兼容层）。
	NewFailoverModel = root.NewFailoverModel
	// NewFallbackChain 是根包 NewFallbackChain 的转发（兼容层）。
	NewFallbackChain = root.NewFallbackChain
	// NewOpenAIProvider 是根包 NewOpenAIProvider 的转发（兼容层）。
	NewOpenAIProvider = root.NewOpenAIProvider
	// NewPriceTable 是根包 NewPriceTable 的转发（兼容层）。
	NewPriceTable = root.NewPriceTable
	// NewResilient 是根包 NewResilient 的转发（兼容层）。
	NewResilient = root.NewResilient
	// NewStageRouter 是根包 NewStageRouter 的转发（兼容层）。
	NewStageRouter = root.NewStageRouter
	// NewUsageHandler 是根包 NewUsageHandler 的转发（兼容层）。
	NewUsageHandler = root.NewUsageHandler
	// PriceOf 是根包 PriceOf 的转发（兼容层）。
	//
	// Deprecated: 同根包——未配价返回 0 与「免费模型」不可区分（静默失真），
	// 且「最长前缀回落」会把同主干异价 SKU 静默定价错。新代码用
	// PriceTable.Lookup / LookupPrice（查不到是「价格未知」而非 0）。
	// 兼容层仍转发它：下游钉在 v0.7.0~v0.8.0 时按旧路径引用它，删掉=编译断。
	PriceOf = root.PriceOf //nolint:staticcheck // 兼容层必须保留已废弃的旧符号（见上方 Deprecated 说明）
	// PriceOfMatched 是根包 PriceOfMatched 的转发（兼容层）。
	//
	// Deprecated: 同 PriceOf——未配价返回 0 与「免费模型」不可区分，前缀回落会静默误定价。
	// 新代码用 PriceTable.Lookup（命中方式可见且不做前缀回落）。
	PriceOfMatched = root.PriceOfMatched //nolint:staticcheck // 兼容层必须保留已废弃的旧符号（见上方 Deprecated 说明）
	// RetryAfterFrom 是根包 RetryAfterFrom 的转发（兼容层）。
	RetryAfterFrom = root.RetryAfterFrom
	// RetryableLLMError 是根包 RetryableLLMError 的转发（兼容层）。
	RetryableLLMError = root.RetryableLLMError
	// SelectRetryDelay 是根包 SelectRetryDelay 的转发（兼容层）。
	SelectRetryDelay = root.SelectRetryDelay
	// SuspiciousEmptyResult 是根包 SuspiciousEmptyResult 的转发（兼容层）。
	SuspiciousEmptyResult = root.SuspiciousEmptyResult
	// WithCallCounter 是根包 WithCallCounter 的转发（兼容层）。
	WithCallCounter = root.WithCallCounter
	// WithRetryAfterSink 是根包 WithRetryAfterSink 的转发（兼容层）。
	WithRetryAfterSink = root.WithRetryAfterSink
	// WithUsageLabels 是根包 WithUsageLabels 的转发（兼容层）。
	WithUsageLabels = root.WithUsageLabels
)

// 包级变量转发。
//
// 注意：变量是**按值复制**的（切片头 / 接口值），
// 与根包共享底层数据，但根包侧的整体重新赋值不会传播到兼容层。
var (
	// BusyAdmissionDelays 是根包 BusyAdmissionDelays 的转发（兼容层）。
	BusyAdmissionDelays = root.BusyAdmissionDelays
	// ErrPriceUnknown 是根包 ErrPriceUnknown 的转发（兼容层）。
	ErrPriceUnknown = root.ErrPriceUnknown
)
