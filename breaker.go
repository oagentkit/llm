package llm

import (
	"github.com/oekit/concurrency/breaker"

	"github.com/oagentkit/llm/port"
)

// 本文件只做**类型别名与薄封装**——实现已下沉到 github.com/oagentkit/llm/port。
// 理由见 budget.go 的说明：熔断的实现不依赖 eino，却原先住在依赖 eino 的根包里。
// 窄接口路径（port.Chain）与 eino 路径（llm.Resilient）因此共用同一套熔断语义。
type breakers = port.Breakers

// 熔断默认值（根包对外承诺的缺省，属 ResilientConfig 的 API 契约）。
const (
	defaultBreakerTrip     = port.DefaultBreakerTrip
	defaultBreakerCooldown = port.DefaultBreakerCooldown
)

func newBreakers(opts ...breaker.BreakerOption) *breakers {
	return port.NewBreakers(opts...)
}
