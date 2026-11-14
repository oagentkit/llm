// 流恢复原语（对标 ZCode streaming-recovery.ts + model-errors.ts）：turn 级
// 流式请求的瞬态错误分类、空终态检测、上下文溢出识别与 busy 准入退避。
// 单请求重试骨架见 errors.go（ClassifyLLMError）与 retryafter.go；本文件补
// 「流断在哪、什么算瞬态、什么时候放弃」的 turn 级语义。
package llm

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

// MaxStreamRecoveryRetries 流断点恢复预算（对标 STREAM_RECOVERY_MAX_RETRIES：
// 跨过单请求重试边界后只恢复 1 次与用户预期差距过大，10 次是生产调过的上限）。
const MaxStreamRecoveryRetries = 10

// transientErrorReasons 瞬态失败原因码/文案白名单（统一小写：匹配前 err 文案
// 会 ToLower；子串匹配，错误链上任意一层包装文案都参与）。
//
// ⚠ llm#190 起本判定改**显式白名单**：不再借 ClassifyLLMError 的
// 「未知缺省可重试」（那是单请求 HTTP 重试的宽进口径——未知文案判瞬态曾让
// turn 级恢复对未知永久错误（内容策略拒绝、function calling 不支持、模型下架
// 等）全开，重开流 10 次、每轮重烧 token 与延迟）。白名单之外的未知文案不判
// 瞬态——重开流的代价是整段重烧，不可判定时宁可让错误尽快上抛。
var transientErrorReasons = map[string]bool{
	// ZCode 形态的原因码
	"model_request_timeout": true, "model_rate_limited": true,
	"model_server_error": true, "model_network_error": true,
	"stream_idle_timeout": true, "rate_limited": true,
	"server_error": true, "network_error": true, "timeout": true,
	// 常见瞬态文案形态（白名单化后未知文案不再缺省瞬态，高频形态显式登记；
	// 与 ClassifyLLMError 的限流/网络面同族，彼处数字 marker 走词边界、此处
	// 放宽为子串）："gateway timeout"/"request timeout" 等由 "timeout" 覆盖
	"rate limit": true, "too many requests": true, "throttl": true,
	"internal server error": true, "bad gateway": true,
	"service unavailable": true, "overloaded": true,
	"connection reset": true, "connection refused": true, "broken pipe": true,
	"temporarily unavailable": true, "deadline exceeded": true,
}

// transientStatusMarkers 裸 5xx 数字形态（"HTTP 502"/"503 service unavailable"）：
// 词边界匹配（errors.go 的 containsMarker 单源——"failed after 1500ms" 不误
// 命中）。429 由 ClassifyLLMError 的限流面覆盖，不入本表。
var transientStatusMarkers = []string{"500", "502", "503", "504", "529"}

// IsTransientStreamError 流式 turn 中的错误是否瞬态（值得从安全锚点重开流）。
// 与 ClassifyLLMError 的分工：后者面向单请求 HTTP 重试、对未知文案缺省可重试
// （宽进）；本判定面向 turn 级恢复——重开流的代价是整段重烧，故取显式白名单
// （llm#190）：结构化瞬态类型（ctx 超时、429/5xx 状态码）+ 原因码/文案白名单
// 命中才判瞬态。未知永久错误（无 marker 文案）不再缺省瞬态——此前最多重开
// 流 10 次、每轮重烧 token 与延迟。
// 调用方主动取消不算瞬态（与降级矩阵「ctx 取消 → 全链中止」一致）：若判为
// 瞬态，取消后的恢复循环会空转 MaxStreamRecoveryRetries 轮、每轮立即再失败。
// ctx 超时算瞬态（挂死流被兜底限时打断，自动恢复语义）。
func IsTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// 结构化 HTTP 状态码：429/5xx 瞬态；其余 4xx 确定性失败，不重开流
	if status := httpStatus(err); status > 0 {
		return status == 429 || status >= 500
	}
	if ClassifyLLMError(err).IsRateLimit {
		return true // 限流文案/数字 marker（词边界，"failed after 1429ms" 不误判）
	}
	msg := strings.ToLower(err.Error())
	for reason := range transientErrorReasons {
		if strings.Contains(msg, reason) {
			return true
		}
	}
	return matchAny(msg, transientStatusMarkers)
}

// SuspiciousEmptyResult 空终态检测：零文本 + 零工具调用 + 非 stop 终止 +
// 零用量 = 可疑空响应（各家 provider 都会偶发；按可重试错误处理而非当正常
// 完结吞掉）。usageTotal 为 provider 报告的总 token（≤0 视为零用量/未知）。
func SuspiciousEmptyResult(finishReason string, responseLen, toolCallCount, usageTotal int) bool {
	if responseLen != 0 || toolCallCount != 0 {
		return false
	}
	fr := strings.TrimSpace(strings.ToLower(finishReason))
	if fr == "stop" || fr == "tool-calls" || fr == "tool_calls" {
		return false
	}
	return usageTotal <= 0
}

// contextExceededMarkers finishReason/错误码层识别上下文溢出（触发 reactive
// 压缩的锚点——见 compact 包）。
var contextExceededMarkers = map[string]bool{
	"context_exceeded": true, "context_length_exceeded": true,
	"context_window_exceeded": true, "model_context_window_exceeded": true,
	"prompt_too_long": true,
}

// IsContextExceededFinish finishReason 层识别上下文溢出。
func IsContextExceededFinish(finishReason, rawFinishReason string) bool {
	return isContextMarker(finishReason) || isContextMarker(rawFinishReason)
}

// IsContextExceededError 错误文案层识别上下文溢出（provider 报文形态）。
func IsContextExceededError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for marker := range contextExceededMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isContextMarker(v string) bool {
	return contextExceededMarkers[strings.TrimSpace(strings.ToLower(v))]
}

// busyProviderCodes provider 繁忙码（zcode-plan 形态：3008/3009/3010——
// 入口排队满，与限流不同类）。
var busyProviderCodes = map[string]bool{"3008": true, "3009": true, "3010": true}

// BusyAdmissionDelays busy 准入重试的缺省两档退避（对标
// START_PLAN_BUSY_MAIN_TURN_ADMISSION_RETRY_DELAYS_MS：1s/2s）。
//
// ⚠ llm#130 起本 var 只是**包初始化缺省的兼容镜像**：BusyAdmissionDelay 的
// 运行时取值读 SetBusyAdmissionDelays 维护的原子快照（热路径无锁读，与替换
// 写不共享内存——写读不再构成 data race，oagentkit/agentkit#167 的上游收口）。
// 直写本 var（reassign / append 重分配 / 置 nil）**不再影响取值**；调优一律
// 走 SetBusyAdmissionDelays。读本 var 只会看到初始化缺省，不反映已生效的表。
var BusyAdmissionDelays = []time.Duration{time.Second, 2 * time.Second}

// busyAdmissionTable busy 准入退避表的运行时快照（llm#130）：原子指针读，
// 与 SetBusyAdmissionDelays 的原子替换构成无竞态读写对——包外直写导出 var
// 的旧用法（oagentkit/agentkit 门面 setter 曾直写，agentkit#167 -race 实测）
// 与热路径不再共享内存。快照持独立的防御性拷贝，与 var 连底层数组都不共享。
var busyAdmissionTable atomic.Pointer[[]time.Duration]

func init() {
	defaultDelays := append([]time.Duration(nil), BusyAdmissionDelays...)
	busyAdmissionTable.Store(&defaultDelays)
}

// ErrEmptyBusyAdmissionDelays 空退避表被拒绝（llm#130）：空表是配置事故而非
// 「关闭退避」的表达（oagentkit/agentkit 门面 setter 的既有口径下沉；要表达
// 「busy 时不等待」请用零时长档位，如 []time.Duration{0}）。
var ErrEmptyBusyAdmissionDelays = errors.New("llm: BusyAdmissionDelays 不能为空表（关闭退避请用零时长档位）")

// SetBusyAdmissionDelays 原子替换 busy 准入退避表（llm#130）。
//
// 并发语义：重试热路径的 BusyAdmissionDelay 无锁读，替换走原子指针写——
// 并发写读不再构成 data race（此前门面 SetBusyAdmissionDelays 直写包级 var
// 与 recovery.go 的运行时读实测 race，见 oagentkit/agentkit#167）。
// 入参防御性拷贝：调用方此后对切片的任何改动（元素写/append 重分配）都不
// 影响已生效的表。空表拒绝（ErrEmptyBusyAdmissionDelays）。
//
// ⚠ 此前直写导出 var BusyAdmissionDelays 的调用方（含 oagentkit/agentkit
// 门面的 setter）必须迁移到本函数：热路径已改读原子快照，直写 var 不再生效。
func SetBusyAdmissionDelays(delays []time.Duration) error {
	if len(delays) == 0 {
		return ErrEmptyBusyAdmissionDelays
	}
	cp := append([]time.Duration(nil), delays...)
	busyAdmissionTable.Store(&cp)
	return nil
}

// IsProviderBusyCode provider 业务码是否「繁忙」类（准入级退避重试的判定；
// 耗尽后调用方应合成不可重试错误，别无限排队）。
func IsProviderBusyCode(code string) bool {
	return busyProviderCodes[strings.TrimSpace(code)]
}

// BusyAdmissionDelay 按 retry 序号（0 起）取准入退避；越界取最后一档；负序号
// 取第一档；**空表返回 0（空表 = 不退避）**——llm#130：修复前空表会把下标
// 算成 `len-1 == -1` 越界 panic（发生在 LLM 重试路径上），现任何输入都
// fail-safe，不下标 -1、不 panic。
//
// 读的是 SetBusyAdmissionDelays 维护的原子快照（race-free）；导出的
// BusyAdmissionDelays var 只是初始化缺省的兼容镜像，直写它不再影响本函数
// 取值。快照正常入口拒绝空表，空表分支是取值函数对快照的自保护。
func BusyAdmissionDelay(retry int) time.Duration {
	tbl := busyAdmissionTable.Load()
	if tbl == nil || len(*tbl) == 0 {
		return 0
	}
	i := max(retry, 0)
	if i >= len(*tbl) {
		i = len(*tbl) - 1
	}
	return (*tbl)[i]
}
