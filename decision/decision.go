// Package decision 是 **System One 协议**（非自回归判定模型）的客户端。
//
// # 它和本仓其它能力的区别
//
// 本仓其它部分都是**自回归生成模型**（chat）：有 messages 概念、输出散文、接
// eino 的 BaseChatModel。System One 协议这一族模型（Jev 托管、Laya 自托管、
// Kev、Clef、CLM…）**不是 chat 模型**：
//
//   - 输入是「待判定状态 + 若干 typed questions」，没有 messages；
//   - 输出是**有限标签 + 概率分布 + 校准置信度**，不生成散文；
//   - 一条状态一次前向 pass 答完全部问题（choice/score/noul 三原语同回）。
//
// 所以它进不了本仓的降级链/预算/成本那套（那是 chat 模型的形状），单独立包。
// 它要回答的问题是「**判**」，不是「**写**」。
//
// # 协议
//
// 请求 `POST {base_url}/v1/systemone`，鉴权 `Authorization: Bearer <key>`：
//
//	请求 {"state": <文本或结构化对象>,
//	      "questions": {"<题名>": {"type": "choice|score|noul",
//	                             "instructions": "问法",
//	                             "criteria": {"choice": {"标签": "判据"},
//	                                         "score":  ["档位0", "档位1"]}}}}
//	响应 {"model": "...",
//	      "answers": {"<题名>": {"type": "choice", "choice": "billing", "confidence": 0.79,
//	                            "probabilities": {"billing": 0.86}},
//	                  "<题名2>": {"type": "noul", "noul": 0.8125},
//	                  "<题名3>": {"type": "score", "score": 1.92, "legend": {"0": "低"},
//	                              "probabilities": {"0": 0.05, "1": 0.72}}},
//	      "usage": {"input_tokens": 349, "output_tokens": 0}}
//
// Jev（托管）与 Laya（自托管）**同协议同载荷**——Laya 官方明言 answer payload 与
// Jev schema-identical ⇒ **换端点即换模型**，本包只实现一套协议。
//
// # 置信度口径必须由本包单源
//
// 真机实测（自托管 Laya 0.4.13，2026-10-23）发现**两个后端的 confidence 给法不一致**：
//
//	Laya  choice: confidence=0.9013（自有公式），另给 x_jev_confidence=0.9712（Jev 口径）
//	Laya  noul:   confidence=0.8125（直接给 p）
//	Jev    noul:   按 Jev 口径给 |2p−1|
//
// 两端各自自洽、都不报错，但**同一门槛在两端含义会漂移**。故本包一律按 Jev 公开
// 公式**从概率分布本地算**（见 Answer.EffectiveConfidence），分布缺失时才回落端点
// 给的 confidence。本地算法与 Laya 的 x_jev_confidence 逐位一致（实测 0.9712 /
// 0.6769）⇒ 公式是被后端实现交叉验证过的，不是纸上推导。
//
// # 纪律
//
//   - **不吞错**：端点不可达/超时/非 2xx 一律返错；「回落还是中断」是调用方的
//     义务（判定面通常是加速件，回落由调用方决定）。
//   - **legend 以真机为准是对象**（官方文档样例写成数组，真机返回对象，数组形态
//     解析会直接失败）。
//   - **mock 模式只验接线不验质量**（见 MockEnv）；真机验收用 decision_live
//     build tag 的可复跑用例。
package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QuestionType 题型（System One 三原语）。
type QuestionType string

const (
	// TypeChoice 选择题：候选集在请求里给（开放词表），返回所选项 + 概率分布 + 置信度。
	TypeChoice QuestionType = "choice"
	// TypeScore 评分题：有序档位在请求里给，返回档位索引（0-indexed）+ 概率分布 + legend。
	TypeScore QuestionType = "score"
	// TypeNoul 是否题：无候选集，返回概率（true 的概率）。
	TypeNoul QuestionType = "noul"
)

// Valid 题型是否合法（组题时校验；非法题型服务端会拒，早失败更省一次往返）。
func (t QuestionType) Valid() bool {
	switch t {
	case TypeChoice, TypeScore, TypeNoul:
		return true
	}
	return false
}

// Question 单题。Criteria 的形态随题型而变：
//
//	choice → map[string]string{候选名: 判定说明}（说明可省）
//	score  → []string{档位描述}（有序，0-indexed）
//	noul   → nil
type Question struct {
	Type         QuestionType `json:"type"`
	Instructions string       `json:"instructions,omitempty"`
	Criteria     any          `json:"criteria,omitempty"`
	// MinConfidence 本题独立门槛（0-1）：低于门槛即弃答（协议支持逐题门槛）。
	MinConfidence *float64 `json:"min_confidence,omitempty"`
}

// Request 一次判定请求（协议请求体）。
//
// State 是待判定状态：string 或结构化对象（两种都被协议接受）。
// Questions 是本次要问的所有题——**一次前向 pass 全答**，这是判定模型的主卖点。
type Request struct {
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
	// Model 指定检查点/模型（留空=服务端按语言与任务自选）。
	Model string `json:"model,omitempty"`
	// MinConfidence 请求级门槛（0-1），逐题门槛之外的兜底。
	MinConfidence *float64 `json:"min_confidence,omitempty"`
	// Task 任务标签（协议可选字段，透传）。
	Task string `json:"task,omitempty"`
	// Lang 语言提示（协议可选字段，透传）。
	Lang string `json:"lang,omitempty"`
	// MaxLen 输入截断上限（协议可选字段，透传）。
	MaxLen int `json:"max_len,omitempty"`
}

// Answer 单题答案。
type Answer struct {
	Type QuestionType `json:"type"`
	// Choice 选择题所选项（标签原文）。
	Choice string `json:"choice,omitempty"`
	// Noul 是否题的概率（true 的概率）。
	Noul *float64 `json:"noul,omitempty"`
	// Score 评分题的档位索引（0-indexed；5 档时 3.87 落在档位 3 与 4 之间）。
	Score *float64 `json:"score,omitempty"`
	// Confidence 端点自算的置信度。**后端口径不一**（见包注释）⇒ 判定面一律走
	// EffectiveConfidence（本地按 Jev 公开公式算），此字段只作原值留档。
	Confidence *float64 `json:"confidence,omitempty"`
	// AnswerConfidence 所选答案的概率（Laya 专有字段；Jev 侧无此键）。
	AnswerConfidence *float64 `json:"answer_confidence,omitempty"`
	// XJevConfidence 端点算好的 Jev 口径置信度（Laya 专有）：与本包本地算法同值，
	// 留作对拍基准——本地算出的值应与它一致。
	XJevConfidence *float64 `json:"x_jev_confidence,omitempty"`
	// Action 动作概率（Laya 专有）。
	Action *ActionInfo `json:"action,omitempty"`
	// Probabilities 概率分布（choice：候选→概率；score：档位索引→概率）。
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	// Legend 评分题的档位表（索引→档位描述）。
	//
	// **形状以真机为准**：官方文档样例里 legend 写作数组，但真机返回的是对象
	// （{"0":"低",…}）——数组形态解析会直接失败（实测）。对象同时兼容两种语义。
	Legend map[string]string `json:"legend,omitempty"`
}

// ActionInfo 动作概率（Laya 专有）。
type ActionInfo struct {
	ActProbability float64 `json:"act_probability"`
}

// EffectiveConfidence 有效置信度——**判定面的单一口径**。
//
// 为什么不直接用端点给的 confidence：**两个后端给法不同**（真机实测 2026-10-23），
// 同一门槛在两端含义会漂移 ⇒ 口径必须在本包：从概率分布按 Jev 公开公式本地计算，
// 分布缺失时才回落端点值。
//
// 公式（Jev 公开口径，与 Laya 的 x_jev_confidence 逐位一致——实测交叉验证：
// choice 3 选 1、p_top=0.9808 ⇒ (3×0.9808−1)/2 = 0.9712；score 4 档 ⇒ 0.6769）：
//
//	choice  confidence = (N·p_top − 1) / (N − 1)
//	          （N=候选数；均匀分布时 0、单点全占时 1；两候选时退化为 p_top−p_second）
//	score   confidence = max(0, 1 − Σᵢ pᵢ·|i − m| / MAD)
//	          （m=最可能档位，档位并列取更小者；MAD=(1/N)·Σᵢ|i−(N−1)/2|）
//	noul    confidence = |2p − 1|（越接近 0.5 越不确信）
//
// 注意口径**随候选集规模变化**：N=20 时 0.5 门槛约等于「p_top > 52.5%」⇒
// 门槛须随候选集标定，不能跨候选集移植。
func (a Answer) EffectiveConfidence() (float64, bool) {
	if a.Type == TypeNoul {
		if a.Noul == nil {
			return 0, false
		}
		p := *a.Noul
		d := 2*p - 1
		if d < 0 {
			d = -d
		}
		return d, true
	}
	if len(a.Probabilities) > 0 {
		switch a.Type {
		case TypeChoice:
			if c, ok := choiceConfidence(a.Probabilities); ok {
				return c, true
			}
		case TypeScore:
			if c, ok := scoreConfidence(a.Probabilities); ok {
				return c, true
			}
		}
	}
	// 分布缺失（或题型不适用）⇒ 回落端点自算值（口径可能与本地不同，但总比没有强）。
	if a.Confidence != nil {
		return *a.Confidence, true
	}
	return 0, false
}

// choiceConfidence 选择题的 Jev 口径置信度：(N·p_top − 1)/(N − 1)。
func choiceConfidence(probs map[string]float64) (float64, bool) {
	n := len(probs)
	if n == 0 {
		return 0, false
	}
	pTop := 0.0
	for _, p := range probs {
		if p > pTop {
			pTop = p
		}
	}
	if n == 1 {
		return 1, true
	}
	c := (float64(n)*pTop - 1) / float64(n-1)
	return clamp01(c), true
}

// scoreConfidence 评分题的 Jev 口径置信度：max(0, 1 − Σ pᵢ·|i−m| / MAD)。
// 档位索引取概率分布的键（"0"/"1"/…，非数字键跳过）；档位不连续时公式不适用。
func scoreConfidence(probs map[string]float64) (float64, bool) {
	idx := make(map[int]float64, len(probs))
	for k, p := range probs {
		i, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		idx[i] = p
	}
	n := len(idx)
	if n == 0 {
		return 0, false
	}
	keys := make([]int, 0, n)
	for i := range idx {
		keys = append(keys, i)
	}
	sort.Ints(keys)
	for i, k := range keys {
		if k != i {
			return 0, false // 档位不连续 ⇒ 公式不适用，回落端点值
		}
	}
	m := keys[0]
	for _, k := range keys {
		if idx[k] > idx[m] {
			m = k
		}
	}
	mad := 0.0
	for _, k := range keys {
		mad += float64(absInt(k - (n-1)/2))
	}
	mad /= float64(n)
	if mad == 0 {
		return 0, false
	}
	dist := 0.0
	for _, k := range keys {
		dist += idx[k] * float64(absInt(k-m))
	}
	return clamp01(1 - dist/mad), true
}

// clamp01 钳位到 [0,1]。
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// absInt 绝对值（避免为一个小函数引 math）。
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Result 一次判定的结果（协议响应体）。
type Result struct {
	// Model 实际应答的模型（服务端可能自选）。
	Model string `json:"model,omitempty"`
	// Answers 逐题答案，键为请求里的题名。
	Answers map[string]Answer `json:"answers"`
	// Usage token 计量（判定模型常 output=0：它不生成文本）。
	Usage Usage `json:"usage"`
}

// Usage token 计量。
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Answer 取单题答案（不存在时 ok=false）。
func (r *Result) Answer(name string) (Answer, bool) {
	if r == nil {
		return Answer{}, false
	}
	a, ok := r.Answers[name]
	return a, ok
}

// ErrNotConfigured 判定面未配置（base_url 缺省等）。调用方据此跳过判定面，
// 不应当作端点故障告警。
var ErrNotConfigured = errors.New("decision: 未配置（base_url 为空）")

// APIError 端点返回的非 2xx。保留状态码与响应体片段（截断）便于排障。
type APIError struct {
	Status  int
	Body    string
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = strings.TrimSpace(e.Body)
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if msg == "" {
		return fmt.Sprintf("decision: 端点返回 HTTP %d", e.Status)
	}
	return fmt.Sprintf("decision: 端点返回 HTTP %d: %s", e.Status, msg)
}

// Config 客户端配置（调用方从自己的配置面映射而来；本包不读环境变量、不读文件）。
type Config struct {
	// BaseURL 端点根地址（不含 /v1/systemone）。空=未配置。
	BaseURL string
	// APIKey 密钥（托管 Jev 必填；自托管未开 key 时可空）。只在内存里，不落盘、不回显。
	APIKey string
	// Model 默认模型/检查点名（留空=服务端自选）。
	Model string
	// Timeout 单次请求超时（0=默认 DefaultTimeout）。判定模型本地是毫秒级，
	// 超时给小了会误杀端点冷启动（首启加载检查点实测数十秒~数分钟）。
	Timeout time.Duration
	// HTTPClient 自定义客户端（测试注入 / 连接池复用）。留空=内部默认。
	HTTPClient *http.Client
}

// DefaultTimeout 缺省超时。判定模型单次调用是毫秒级（本地实测 165-211ms），
// 10s 给端点冷启动与网络抖动留余量。
const DefaultTimeout = 10 * time.Second

// Path 系统一协议端点路径（相对 BaseURL）。
const Path = "/v1/systemone"

// Client 判定面客户端。并发安全（http.Client 自身并发安全，本包无共享可变态）。
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// New 按配置建客户端。base_url 为空 ⇒ 返回 ErrNotConfigured（调用方据此跳过
// 判定面，走原有路径——不静默降级成「配了但不生效」）。
func New(cfg Config) (*Client, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return nil, ErrNotConfigured
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	return &Client{
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  strings.TrimSpace(cfg.APIKey),
		model:   strings.TrimSpace(cfg.Model),
		http:    hc,
	}, nil
}

// BaseURL 端点根地址（不含密钥）。
func (c *Client) BaseURL() string { return c.baseURL }

// Model 默认模型名（可能为空=服务端自选）。
func (c *Client) Model() string { return c.model }

// Decide 发一次判定请求。
//
// 失败语义（**一律返错，不静默吞**）：端点不可达 / 超时 / 非 2xx / 响应体不合法
// ⇒ 返回 error，调用方决定回落。已应答但某题缺答案不返错——那属于「模型没答」
// （由调用方按题名取答案 + 置信门槛处理）。
func (c *Client) Decide(ctx context.Context, req Request) (*Result, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	if len(req.Questions) == 0 {
		return nil, errors.New("decision: 请求没有题目（questions 为空）")
	}
	for name, q := range req.Questions {
		if !q.Type.Valid() {
			return nil, fmt.Errorf("decision: 题目 %q 题型非法: %q", name, q.Type)
		}
	}
	if mockEnabled() {
		// mock 模式：不发网络请求，本地合成答案（验接线用）。
		return &Result{Model: "mock", Answers: mockAnswers(req), Usage: Usage{InputTokens: 32}}, nil
	}
	body := req
	if body.Model == "" {
		body.Model = c.model
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("decision: 请求序列化失败: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+Path, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("decision: 请求构造失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("decision: 端点调用失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("decision: 响应读取失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Body: string(raw)}
	}
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decision: 响应解析失败: %w", err)
	}
	return &out, nil
}

// Warmup 预热端点（加载检查点）。
//
// 为什么需要：判定端点的**首次**调用要加载检查点（自托管实测首启数十秒~数分钟），
// 不预热则首个真实请求必然撞超时上限 ⇒ 表现为「配了判定面但行为一点没变」的
// 静默假生效。预热是**尽力而为**：失败只返错误，由调用方决定记不记警——真正的失败
// 会在真调用时照常暴露。
//
// 用最小题（一个 noul）打一发即可：只为等端点就绪，不求判定质量。
func (c *Client) Warmup(ctx context.Context) error {
	_, err := c.Decide(ctx, Request{
		State: "warmup probe",
		Questions: map[string]Question{
			"ping": {Type: TypeNoul, Instructions: "Is this a warmup probe?"},
		},
	})
	return err
}

// MockEnv 开关：置 "1" 时客户端**不发网络请求**，本地合成固定答案。
//
// 用途与边界：mock 验的是**接线**（判定面装上了吗、快路径真会接管吗、出参形态
// 对吗），**不是模型质量**——判定质量由真机用例（build tag decision_live）背书。
// 别拿 mock 的准确率说事。
const MockEnv = "BQ_DECISION_MOCK"

// mockEnabled mock 模式是否开启。
func mockEnabled() bool {
	v := strings.TrimSpace(os.Getenv(MockEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// mockAnswers 本地合成答案：choice 取候选集首个（题名为 route 时优先取
// "fault-diagnosis"——确定性条目，让 e2e 断言稳定）；score 给中间档；noul 给 0.95。
func mockAnswers(req Request) map[string]Answer {
	out := make(map[string]Answer, len(req.Questions))
	for name, q := range req.Questions {
		switch q.Type {
		case TypeChoice:
			pick := firstCandidate(q.Criteria)
			if name == "route" {
				if _, ok := q.Criteria.(map[string]string)["fault-diagnosis"]; ok {
					pick = "fault-diagnosis"
				}
			}
			probs := map[string]float64{}
			if pick != "" {
				probs[pick] = 1.0
			}
			conf := 1.0
			out[name] = Answer{Type: TypeChoice, Choice: pick, Confidence: &conf, Probabilities: probs}
		case TypeScore:
			levels, _ := q.Criteria.([]string)
			mid := float64(len(levels) / 2)
			probs := map[string]float64{}
			for i := range levels {
				probs[strconv.Itoa(i)] = 0.0
			}
			conf := 0.6
			out[name] = Answer{Type: TypeScore, Score: &mid, Confidence: &conf, Probabilities: probs}
		case TypeNoul:
			p := 0.95
			out[name] = Answer{Type: TypeNoul, Noul: &p, Confidence: &p}
		}
	}
	return out
}

// firstCandidate 取候选集首个标签（choice 的 criteria 是 map[string]string；
// 判据可省，故按 map 键序取首个稳定项）。
func firstCandidate(criteria any) string {
	m, ok := criteria.(map[string]string)
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}
