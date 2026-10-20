package llm

import "github.com/oagentkit/llm/port"

// 本文件只做**类型别名与薄封装**——实现已下沉到 github.com/oagentkit/llm/port。
//
// 为什么下沉：预算/成本/价表/熔断的实现本身**不依赖 eino**，但它们原先住在根包
// llm 里，而根包因为 client.go 依赖 eino 而「整包不可用」——非 eino 生态的宿主
// 连「记一笔 token」都用不上本仓的能力。下沉后，根包与窄接口层（port.Chain）
// 共用**同一份实现**，两条路径的预算/成本口径完全一致（不会出现两套算账）。
//
// 用类型别名（=）而不是新定义：llm.Budget 与 port.Budget 是**同一个类型**，
// 根包的对外 API 一字未变，调用方的类型断言与转换全部照旧生效。
type (
	// Budget 任务级 token 预算累计器（TokenAccountant 的标准实现）。
	Budget = port.Budget
	// TokenAccountant token 记账接口。
	TokenAccountant = port.TokenAccountant
)

// NewBudget 创建预算累计器。
func NewBudget(limit int) *Budget { return port.NewBudget(limit) }
