package port

import "sync"

// TokenAccountant token 记账接口（调用方可接预算累计器或 Prometheus）。
//
// 原先定义在根包 client.go 里——而根包因为 client.go 依赖 eino 而「整包不可用」，
// 非 eino 生态的宿主连「记一笔 token」都做不到。下沉到这里后，根包用类型别名
// 保持 API 不变（llm.TokenAccountant == port.TokenAccountant）。
type TokenAccountant interface {
	// Add 累计消耗。
	Add(n int)
	// Used 已消耗。
	Used() int
	// Remaining 剩余额度（不低于 0）。
	Remaining() int
}

// Budget 任务级 token 预算累计器（TokenAccountant 的标准实现）。
// 并发安全。
type Budget struct {
	mu    sync.Mutex
	limit int
	used  int64
}

// NewBudget 创建预算累计器。
func NewBudget(limit int) *Budget {
	return &Budget{limit: limit}
}

// Add 累计消耗。
func (b *Budget) Add(n int) {
	b.mu.Lock()
	b.used += int64(n)
	b.mu.Unlock()
}

// Used 已消耗。
func (b *Budget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.used)
}

// Remaining 剩余额度（不低于 0）。
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.limit - int(b.used)
	if r < 0 {
		return 0
	}
	return r
}

// Limit 预算上限。
func (b *Budget) Limit() int { return b.limit }

var _ TokenAccountant = (*Budget)(nil)
