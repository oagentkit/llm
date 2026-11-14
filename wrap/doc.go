// Package wrap eino BaseChatModel 装饰器集：窗口拟合（FitModel）、输出截断续写
// （ContinueModel）、限速（RateModel）、在飞闸（GovernorModel）、熔断
// （BreakerModel）。五者职责正交、可任意组合。
//
// 推荐组装顺序（内→外）：Rate→Governor→Breaker→Fit→Continue。
//
//   - Rate 最内层不变：物理调用同桶（与 Generator 链共享全局令牌桶；续写轮
//     的再调用同样过桶）；
//   - Governor 次内层：在飞名额覆盖「限速等待 + 真实调用」整段——一次完整
//     发送尝试才算一次在飞；排队与排队中止都发生在闸门处，不触达内层；
//   - Breaker 在治理层之外：开闸快速失败先于限速等待与在飞排队（不烧全局
//     令牌、不占在飞名额），且闸门排队中止不计入熔断失败统计。代价：半开
//     探测窗口含闸内等待，队列量级大时经 WithProbeTimeout 显式调大（代绑定
//     保证失联误判不污染统计）；
//   - Fit 在治理层之外：先拟合后治理——发送的是拟合后的输入，闸/断/速统计
//     针对真实发送；
//   - Continue 最外层：续写轮再入全栈（续写也是物理调用，同样过闸/断/速）。
//
// WithTools 派生共享状态（issue #198 契约，Governor/Breaker 特有）：eino
// ReAct 每轮重新派生带工具模型，五装饰器的 WithTools 均做绑定透传再包装；
// 其中 Governor/Breaker 携带可变状态，派生时**必须原样传递状态指针**——
// 「派生即新状态」= 闸门/熔断失效（每轮一枚新闸/新熔断器，封顶与连挂计数
// 形同虚设）。两者均已按此落地并有测试钉死；外部自行实现同类装饰器时同此契约。
//
// 与 llm.Client 的分工：Client.fitInput 服务 Generator 链（单级截最长 user，
// 内嵌拟合）；本包装饰器服务直调旁路（ReAct/RawModel 等裸 BaseChatModel），
// 三级递进且带真实 usage 校准。同进程两路并存无副作用（拟合只紧不松、幂等）。
//
// 收编履历：Fit/Continue/Rate 收编自 argus internal/context（v0.10.32），
// 在 PR 审查Bot 生产验证 ~1 个月（v3.36.0–v3.75）；Governor/Breaker 收编自
// bianque internal/llm 的手搓装饰器（issue #198）——内核本就单源在家族
// （syncx.KeyedGate / concurrency/breaker），收编只补 eino 装饰壳；熔断回填
// 纪律与 port.Breakers（llm.Resilient）同款。观测依赖已回调化（OnFit/
// OnContinue/OnBreakdown 可 nil；Governor/Breaker 的观测经内核选项接入）。
package wrap
