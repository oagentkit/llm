// Package llm 是历史 import 路径 github.com/oagentkit/llm/llm 的**兼容层**。
//
// # 这个路径曾经真实存在
//
// llm 的根包在 v0.7.0 ~ v0.8.0 期间住在仓内 llm/ 子目录，import 路径是
// github.com/oagentkit/llm/llm。v0.9.0（2026-10-19）把根包上移到 module 根，
// 路径改成 github.com/oagentkit/llm，并在 CHANGELOG 里标了 BREAKING CHANGE。
//
// 任何在 v0.7.x ~ v0.8.x 期间把 llm 钉进 go.mod 的下游，都会留下一批
// `github.com/oagentkit/llm/llm` 的 import。升级到 v0.9.0+ 时这些 import
// 全部编译失败——即使它们用到的**符号一个都没变**（API 面零漂移，变的只是
// import 路径这一层）。
//
// # 为什么提供这一层
//
// 让存量下游跨过 v0.9.0 这条线时不必整仓改 import：老路径继续可用，
// 新路径也可用。改一次 import 路径要动下游的每个引用文件，还可能与下游
// 自己正在做的改动撞车；把「改名」换成「二选一」，成本低得多。
//
// # 语义：与根包零差异
//
// 本包不含任何自己的实现：
//
//   - 类型是 type alias，与根包是**同一个类型**（不是新定义、不是结构相同的两份）；
//   - 函数以 var 形式转发根包的包级函数，拿到的是**同一个函数值**；
//   - 常量原样转发；
//   - 包级变量按值复制（切片头 / 接口值），与根包共享底层数据，
//     但根包侧的整体重新赋值不会传播到本包。
//
// # 新代码不要用这一层
//
// 新代码一律直接 import github.com/oagentkit/llm。
//
// 本包是**过渡期**产物：只镜像根包已有的符号，不新增任何能力；
// 与门面仓 github.com/oagentkit/agentkit 一样属于「只为存量下游存在」的兼容层，
// 随 0.x 版本线退役。
//
// # 一致性由测试强制
//
// compat_test.go 的 TestCompatParity 会解析根包与本包的 AST，
// 要求两者的导出符号集合**完全一致**：根包增删了导出符号而本包没跟上时，
// 那条测试会红。
package llm
