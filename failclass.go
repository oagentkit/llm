package llm

// 本文件是**llm 错误族 → guardx/toolfail 处置类别**的机械映射（llm#234）。
//
// # 两套分类各答什么（对标 toolgate/policy/failclass.go 的单源纪律）
//
//	|                        | `llm.RetryHint`              | `guardx/toolfail.Class` |
//	|------------------------|------------------------------|--------------------------|
//	| 判什么                 | 模型调用层的**重试维度**      | 处置层**语义**            |
//	| 取值                   | Retryable / IsRateLimit / IsTruncated（三维 bool） | transient / permanent / rate_limited / …（八类） |
//	| 回答                   | 「这个失败值不值得原样重来」  | 「该不该重试、能不能原样重发、要不要退避」 |
//
// 二者不是重复实现，而是**同一件事的两个问题**。本文件给出两者之间那一步
// **机械映射**：它必须只有一份，散在各调用点手写必然漂移。
//
// # 输入是 error 而不是 hint（判据体与取数分离的反面——刻意的）
//
// 取 error 而不是已算好的 RetryHint，是为了让**取消特判与分类学同室**：
// ClassifyLLMError 对 context.Canceled 已收口为 RetryHint{}（llm#234 判据②），
// 但 RetryHint{} 同时也代表「确定性 4xx」——只看 hint 分不开「用户取消」与
// 「参数非法」，两者的处置指引不同（取消不该叫宿主去修声明）。持 err 才能
// errors.Is 先把取消摘出来。分类仍只有一份：hint 由 ClassifyLLMError 现算，
// 调用方不可能传入与 err 不匹配的 hint。
//
// # 映射的取舍（三维 → 类，不是一对一）
//
//   - IsRateLimit → rate_limited：可重试 + 必须退避（toolfail 该类自带退避语义）；
//   - !Retryable  → permanent：确定性失败（鉴权/参数/模型不存在/上下文超限），
//     重试纯浪费——与 toolfail 的「取不可重试一侧」保守纪律同向；
//   - 其余 Retryable（含 IsTruncated、模型超时、5xx/网络抖动）→ transient：
//     可重试，但**默认不给重发安全**（SafeToResubmit=false——服务端可能已执行，
//     截断类尤其要先改参数再重来），正合 llm 的语义（#222 三轴判据①：
//     模型超时 → ClassTransient）。
//
// # 值必须与 guardx/toolfail Class 逐字一致（同步义务）
//
// 这些字符串是 `toolfail.Class` 的字面量形态（Class 是 string 类型，宿主可
// 直接 `toolfail.Class(ToolfailClass(err))` 类型转换使用）。两仓不能互 import
// （方向与分层），编译期钉不住 ⇒ **改 toolfail 任一类名时必须同步本表**；
// 本表取值是冻结常量，test_failclass 逐字钉死（改名先改测试再改表，红了
// 就是忘了同步）。取未知类名的宿主侧后果是 PolicyOf 零值（不可重试）——
// 漂移的失败方向是保守侧，不是误重试。

import (
	"context"
	"errors"
)

// ToolfailClass 把一个 llm 错误映射到 guardx/toolfail 的处置类别名。
//
// 纯函数：结论只由 err 决定（分类走 ClassifyLLMError 单源），不做 I/O、
// 不读包级可移状态——同一输入恒得同一输出，可单独测。
//
// 取消 → "unknown"（不是 permanent）：取消不是「失败」，没有声明要修；
// 落 unknown = 不可分类即不可重试，处置上与 permanent 同向（都不重试），
// 语义上不冒充确定性失败（判据②：取消不进瞬时类，也不进误导性的确定类）。
func ToolfailClass(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, context.Canceled) {
		return "unknown"
	}
	hint := ClassifyLLMError(err)
	switch {
	case hint.IsRateLimit:
		return "rate_limited"
	case !hint.Retryable:
		return "permanent"
	default:
		return "transient"
	}
}
