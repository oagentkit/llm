package llm

import "github.com/oagentkit/llm/port"

// 本文件只做**类型别名与薄封装**——实现已下沉到 github.com/oagentkit/llm/port。
// 理由见 budget.go 的说明：成本记账的实现不依赖 eino，却原先住在依赖 eino 的根包里，
// 宿主（非 eino 生态）因此用不上。类型别名保证 llm.CostTracker 与
// port.CostTracker 是**同一个类型**，两条路径共用一份成本账本。
type (
	// CostRecord 单次调用成本记录。
	CostRecord = port.CostRecord
	// CostTracker token 成本追踪器。
	CostTracker = port.CostTracker
	// ModelSummary 单模型汇总。
	ModelSummary = port.ModelSummary
)

// NewCostTracker 创建成本追踪器（用内建价表）。
func NewCostTracker() *CostTracker { return port.NewCostTracker() }

// NewCostTrackerWithTable 创建成本追踪器（指定价表）。
func NewCostTrackerWithTable(table *port.PriceTable) *CostTracker {
	return port.NewCostTrackerWithTable(table)
}
