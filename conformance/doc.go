// Package conformance 是 port.Completer 窄接口的共享契约测试套件：把
// 「一个 Completer 实现接入降级链后必须满足的行为约定」写成可复用的场景集，
// 供第三方后端实现方直接引入做验收——新后端 go test 跑同一套即可自查合规。
//
// 套件不是测试替身（llmtest 保持原职，那是单测打桩），而是面向外部实现者的
// 合规验证：每个子测试钉一条语义，任何子测试失败即实现违反契约。
//
// 用法（三行）：
//
//	func TestMyCompleter(t *testing.T) {
//	    conformance.Run(t, myFactory) // myFactory 按 Behavior 构造你的 Completer
//	}
//
// 生命周期: Experimental——本包仍会改、删、拆，不承诺任何兼容，升级风险自负；
// 定级理由见 api-lifecycle.yaml，分级规则见 docs/api-compat.md。
package conformance
