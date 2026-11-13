package llm

import (
	"errors"
	"strings"

	openai "github.com/meguminnnnnnnnn/go-openai"
)

// RetryHint 错误分类结果。
type RetryHint struct {
	Retryable   bool // 是否值得重试
	IsRateLimit bool // 是否 429/rate limit
	IsTruncated bool // 是否输出截断
}

// nonRetryable4xx 确定性失败的 HTTP 状态码（408/429 除外——超时/限速值得重试）。
var nonRetryable4xx = []string{"400", "401", "402", "403", "404", "405", "413", "422"}

// marker 表预编译：原先每次 ClassifyLLMError 都现构 slice 字面量（失败路径
// 一次调用链要分类 2~3 次）。
var (
	truncMarkers = []string{"finish_reason=length", "输出被截断", "output truncated"}
	authMarkers  = []string{"401", "403", "unauthorized", "invalid api key", "invalid_api_key", "forbidden"}
	// det4xxMarkers 注意「does not exist」必须带 model 邻接（rag#64 同族教训：
	// 裸 "does not exist" 把 DNS 瞬时失败「name does not exist」判成确定性失败
	// ⇒ 放弃自愈；裸 marker 的漏判方向比误重试更坏）。
	det4xxMarkers = []string{"bad request", "invalid request", "invalid parameter", "invalid_parameter", "model not found", "unknown model", "no such model", "model does not exist"}
	// ctxLenMarkers 同理：「too long」裸词把网关超时「upstream took too long」
	// 判成上下文超限 ⇒ 不重试。收窄为「is/input/context + too long」邻接形态。
	ctxLenMarkers  = []string{"context length", "context_length", "maximum context", "max context", "is too long", "input too long", "context too long", "too many tokens", "reduce the length", "input length"}
	rateLimitMarks = []string{"429", "rate limit", "ratelimit", "too many requests", "throttl"}
)

// ClassifyLLMError 判定是否值得重试及错误类型。
// 分类优先级：结构化 HTTP 状态码（go-openai RequestError）→ 文本 marker。
// 确定性失败（鉴权/参数错/模型不存在/上下文超限等 4xx）不重试——重试纯浪费
// 钱和延迟；429 可重试 + 退避下限提高；5xx/网络错误默认可重试。
func ClassifyLLMError(err error) RetryHint {
	if err == nil {
		// nil 语义与 RetryableLLMError/IsRateLimitError/IsTruncatedError 对齐
		//（oagentkit/llm#65）：nil 无失败可言，RetryHint{} 即「不可重试」——
		// 此前这里返回 Retryable:true 与同文件 RetryableLLMError(nil)=false
		// 自相矛盾，成功路径误传 nil 会被当成「可重试的失败」。
		return RetryHint{}
	}
	// 结构化状态码优先：不受错误文案措辞影响
	if hint, ok := classifyByHTTPStatus(err); ok {
		return hint
	}
	msg := strings.ToLower(err.Error())
	// 截断 marker 必须最先判：Client 生成的截断错误文本含 "completion_tokens=<n>"
	// 数字，若先走下方数字状态码匹配，n 恰为 401/429 等值时会误判为鉴权失败/
	// 限速，"截断→提升 MaxOutputTokens 重试"机制确定性失效。
	if matchAny(msg, truncMarkers) {
		return RetryHint{Retryable: true, IsTruncated: true}
	}
	if isDeterministicFailure(msg) {
		return RetryHint{}
	}
	if matchAny(msg, rateLimitMarks) {
		return RetryHint{Retryable: true, IsRateLimit: true}
	}
	return RetryHint{Retryable: true}
}

// classifyByHTTPStatus 结构化 HTTP 状态码判定（4xx 区间）。ok=false 表示无状态码，
// 退回文本 marker 分类。
func classifyByHTTPStatus(err error) (RetryHint, bool) {
	status := httpStatus(err)
	if status < 400 || status >= 500 {
		return RetryHint{}, false
	}
	switch status {
	case 408:
		return RetryHint{Retryable: true}, true
	case 429:
		return RetryHint{Retryable: true, IsRateLimit: true}, true
	default:
		return RetryHint{}, true
	}
}

// isDeterministicFailure 文本判定确定性失败：鉴权/权限、参数错/模型不存在、
// 非 408/429 的 4xx 状态码、上下文超限——任一命中即不重试。
func isDeterministicFailure(msg string) bool {
	if matchAny(msg, authMarkers) {
		return true
	}
	if matchAny(msg, det4xxMarkers) {
		return true
	}
	if matchAny(msg, nonRetryable4xx) {
		return true
	}
	return matchAny(msg, ctxLenMarkers)
}

// matchAny 任一 marker 命中即真。
func matchAny(msg string, markers []string) bool {
	for _, marker := range markers {
		if containsMarker(msg, marker) {
			return true
		}
	}
	return false
}

// httpStatus 提取结构化 HTTP 状态码（0 = 未知）。
// 优先 errors.As 提取 go-openai 的 RequestError；错误链经 eino/网关包装后仍可穿透。
func httpStatus(err error) int {
	if re, ok := errors.AsType[*openai.RequestError](err); ok {
		return re.HTTPStatusCode
	}
	return 0
}

// containsMarker 文本 marker 匹配。数字状态码用词边界匹配——边界取「前后
// 非字母数字」："429" 不得命中 "failed after 1429ms"（相邻数字），也不得命中
// "upstream timeout after 400ms"/"failed after 429ms"（字母单位后缀，llm#78）。
// 后边界排除「小数点+数字」形态（llm#114）："timeout after 429.53ms" 的 "429"
// 曾按边界成立 → 假阳性限流白等退避、"row 401.2" → 假阳性鉴权放弃重试；
// 小数点后跟数字时是数字的一部分，不是边界（"429. " 句末句点仍按边界命中）。
// 前侧对称排除「数字+小数点」形态（llm#125）：marker 是某小数的分数尾时
// 同样不是独立状态码——"elapsed 12.429 total" 曾假阳性限流、"12.401" 曾
// 假阳性确定性失败。
func containsMarker(msg, marker string) bool {
	if !isDigitToken(marker) {
		return strings.Contains(msg, marker)
	}
	for i := 0; i+len(marker) <= len(msg); i++ {
		if msg[i:i+len(marker)] != marker {
			continue
		}
		before, after := byte(' '), byte(' ')
		if i > 0 {
			before = msg[i-1]
		}
		if i+len(marker) < len(msg) {
			after = msg[i+len(marker)]
		}
		if !isAlnumByte(before) && !isAlnumByte(after) {
			if isDecimalContinuation(msg, i+len(marker)) {
				continue
			}
			if isDecimalTail(msg, i) {
				continue
			}
			return true
		}
	}
	return false
}

// isDecimalTail 判定 msg[pos-1] 处是否「数字+小数点」形态（llm#125，#114
// 后侧排除的镜像侧）：marker 前是小数点、小数点前还是数字 ⇒ marker 是某个
// 小数的分数尾（"elapsed 12.429 total" 的 "429"），不是独立状态码。
func isDecimalTail(msg string, pos int) bool {
	return pos >= 2 && msg[pos-1] == '.' && isDigitByte(msg[pos-2])
}

// isDecimalContinuation 判定 msg[pos] 起是否「小数点+数字」形态（llm#114）：
// 数字 marker 后的小数点是小数的一部分（"429.53ms"/"401.2"），不是状态码边界。
func isDecimalContinuation(msg string, pos int) bool {
	return pos+1 < len(msg) && msg[pos] == '.' && isDigitByte(msg[pos+1])
}

// isAlnumByte ASCII 字母或数字——数字状态码的词内成分（"400ms"/"a429b" 的
// 字母与数字一样构成词内部，不是状态码边界；汉字等多字节首字节 ≥0x80 不在
// 此列，"错误429限流" 仍按边界命中）。
func isAlnumByte(b byte) bool {
	return isDigitByte(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isDigitToken(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// IsRateLimitError 快速判定是否 429 类错误。
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	return ClassifyLLMError(err).IsRateLimit
}

// IsTruncatedError 快速判定是否输出截断错误。
func IsTruncatedError(err error) bool {
	if err == nil {
		return false
	}
	return ClassifyLLMError(err).IsTruncated
}

// RetryableLLMError 判定错误是否值得重试。
func RetryableLLMError(err error) bool {
	if err == nil {
		return false // 与 IsRateLimitError/IsTruncatedError 的 nil 语义对齐：nil 无失败可言
	}
	return ClassifyLLMError(err).Retryable
}
