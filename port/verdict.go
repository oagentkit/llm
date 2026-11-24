package port

import "errors"

// 重试判定结论的外泄面——llm 侧「该不该重试」汇入宿主分类单源的唯一出口。
//
// # 为什么是这一处
//
// llm 侧「该不该重试」的判定只有一份：Classifier 给出的 bool 结论。
// 此前该结论只用于链内决定「重试本环节还是切下一环」，随错误出链时被丢弃
// ⇒ 宿主拿不到结构化结论，重试分类散成第二套。
//
// 本文件把结论外泄成宿主能读到的形态：
//
//  1. 错误装饰（主出口）：RetryVerdictError 实现宿主错误分类器
//     （guardx/toolfail 的 RetryableReporter）的鸭子契约——宿主用
//     toolfail.Classify(err) / toolfail.Retryable(err) 读到与 llm 内部
//     同源的结论。本包**不 import guardx**：契约按**结构**（方法集）对接，
//     与本仓既有先例同款（根包 RetryExhaustedError.Retryable() 亦是鸭子实现）。
//  2. 直接透出口（辅出口）：RetryVerdictOf 读回同一条结论，供不引入宿主
//     分类器的宿主使用。
//
// 两条出口读的是同一处结论，不构成第二套分类学。
//
// # 判据体与取数分离
//
// 判据体（verdictOf）是纯函数：结论只由 (classify, err) 决定——不改入参、
// 不做 I/O、不读包级可变状态 ⇒ 同一输入恒得同一结论，可单独测。
// 取数件与判据分开：分类表 = Classifier（注入，链上可替换），
// 错误包装 = RetryVerdictError（复用既有件，不在调用点现造）。

// RetryVerdict 一条重试判定结论——llm 侧「该不该重试」的唯一答案形态。
type RetryVerdict struct {
	// Retryable 该错误是否值得原样重试。
	Retryable bool
}

// RetryVerdictError 把一条重试判定结论外泄到错误链上。
//
// 宿主错误分类器（如 guardx/toolfail 的 RetryableReporter）沿错误链
// errors.As 找任意一环的 Retryable() 方法即可读到结论，无需本包 import
// 那个仓所在的包。
//
// 文案透传底层错误（结论不进文案）：结论是结构面信息，文案会变；把结论
// 混进文案会让「按文案聚合」与「按结论处置」两套口径互相污染。
type RetryVerdictError struct {
	// Err 底层错误（Unwrap 返回它 ⇒ errors.Is/As 仍穿透到原始错误）。
	Err error
	// Verdict 该错误的判定结论。
	Verdict RetryVerdict
}

// Error 实现 error：文案与底层错误一致（见类型注释）。
func (e *RetryVerdictError) Error() string {
	if e == nil || e.Err == nil {
		return "<nil>"
	}
	return e.Err.Error()
}

// Unwrap 交出底层错误，errors.Is/As 判定链不因装饰而断。
func (e *RetryVerdictError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Retryable 自报该错误的重试结论——宿主分类器（guardx/toolfail 的
// RetryableReporter）按结构命中本方法即可读到与 llm 内部同源的结论。
//
// nil 接收者返回 false 且不解引用：宿主分类器会对错误链上每一环直接调本方法，
// 链上可能挂着 typed-nil 指针。
func (e *RetryVerdictError) Retryable() bool {
	if e == nil {
		return false
	}
	return e.Verdict.Retryable
}

// retryableReporter 宿主错误分类器（如 guardx/toolfail 的 RetryableReporter）
// 的鸭子契约形状。
type retryableReporter interface {
	Retryable() bool
}

// 编译期断言：结论载体满足宿主分类器的鸭子契约。
var _ retryableReporter = (*RetryVerdictError)(nil)

// RetryVerdictOf 从错误链上读回 llm 侧的重试判定结论。
//
// 宿主不想引入宿主分类器时的直接透出口：读的是与错误装饰面**同一处**结论，
// 不构成第二套分类学。ok=false 表示链上没有 llm 的判定结论（链外错误）。
func RetryVerdictOf(err error) (RetryVerdict, bool) {
	v, ok := errors.AsType[*RetryVerdictError](err)
	if !ok || v == nil {
		return RetryVerdict{}, false
	}
	return v.Verdict, true
}

// verdictOf 判定一条错误是否值得原样重试（纯函数，见文件注释）。
//
// classify 为 nil 时取保守缺省（不可重试）——与宿主分类器
// 「不可分类 = 不可重试」的取舍同向。
func verdictOf(classify Classifier, err error) RetryVerdict {
	if err == nil || classify == nil {
		return RetryVerdict{}
	}
	return RetryVerdict{Retryable: classify(err)}
}

// withVerdict 把判定结论包进错误形态（错误包装件，调用点复用，不现造）。
//
// err 为 nil 时原样返回 nil（没有失败就没有结论可带）。
func withVerdict(err error, v RetryVerdict) error {
	if err == nil {
		return nil
	}
	return &RetryVerdictError{Err: err, Verdict: v}
}
