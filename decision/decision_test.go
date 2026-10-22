package decision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeEndpoint 起一个假 System One 端点（协议同 Jev/Laya），回放给定 answers。
func fakeEndpoint(t *testing.T, status int, answers string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"fake-1","answers":` + answers +
			`,"usage":{"input_tokens":80,"output_tokens":0}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDecideProtocolWire 请求面逐字钉：路径、鉴权头、请求体形状。
// 接错字段是**静默失真**（端点照常应答，只是没按你以为的问法答）。
func TestDecideProtocolWire(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fake-1","answers":{"pick":{"type":"choice","choice":"a","confidence":0.9}},` +
			`"usage":{"input_tokens":10,"output_tokens":0}}`))
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, APIKey: "k-test", Model: "m-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	thr := 0.5
	res, err := c.Decide(context.Background(), Request{
		State: map[string]any{"body": "待判定文本"},
		Questions: map[string]Question{
			"pick": {Type: TypeChoice, Instructions: "选哪个？", Criteria: map[string]string{"a": "甲", "b": "乙"}},
		},
		MinConfidence: &thr,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if gotPath != Path {
		t.Errorf("path = %q, want %q", gotPath, Path)
	}
	if gotAuth != "Bearer k-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if got, ok := gotBody["state"].(map[string]any); !ok || got["body"] != "待判定文本" {
		t.Errorf("state 形状不对: %#v", gotBody["state"])
	}
	q, ok := gotBody["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions 未序列化: %#v", gotBody["questions"])
	}
	pick, ok := q["pick"].(map[string]any)
	if !ok || pick["type"] != "choice" || pick["instructions"] != "选哪个？" {
		t.Errorf("题面不对: %#v", pick)
	}
	if gotBody["model"] != "m-1" {
		t.Errorf("model = %#v, want 客户端默认模型", gotBody["model"])
	}
	if gotBody["min_confidence"] != 0.5 {
		t.Errorf("min_confidence = %#v", gotBody["min_confidence"])
	}
	if a, ok := res.Answer("pick"); !ok || a.Choice != "a" {
		t.Errorf("答案解析错: %#v", res.Answers)
	}
}

// TestDecideGoldenResponse 对拍官方文档给出的响应样例（逐字）：三原语同回。
func TestDecideGoldenResponse(t *testing.T) {
	const golden = `{
	  "model": "jev-1.13.0",
	  "answers": {
	    "is_ok": {"type": "noul", "noul": 0.95},
	    "which": {
	      "type": "choice",
	      "choice": "alpha",
	      "confidence": 0.79,
	      "probabilities": {"alpha": 0.86, "beta": 0.14, "gamma": 0.0}
	    }
	  },
	  "usage": {"input_tokens": 349, "output_tokens": 58}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(golden))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Decide(context.Background(), Request{
		State: "一段待判定文本",
		Questions: map[string]Question{
			"is_ok": {Type: TypeNoul, Instructions: "是否通过？"},
			"which": {Type: TypeChoice, Instructions: "选哪个？", Criteria: map[string]string{"alpha": "", "beta": "", "gamma": ""}},
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(res.Answers) != 2 {
		t.Fatalf("答案数 = %d, want 2", len(res.Answers))
	}
	ok, _ := res.Answer("is_ok")
	if ok.Type != TypeNoul || ok.Noul == nil || *ok.Noul != 0.95 {
		t.Errorf("noul 答案不对: %#v", ok)
	}
	// noul 的有效置信度取 |2p−1|（不吃端点给的 noul confidence——两后端口径不一）。
	if conf, has := ok.EffectiveConfidence(); !has || conf < 0.8999 || conf > 0.9001 {
		t.Errorf("noul 有效置信度 = %v(%v), want 0.9", conf, has)
	}
	which, _ := res.Answer("which")
	if which.Choice != "alpha" {
		t.Errorf("choice = %q", which.Choice)
	}
	if len(which.Probabilities) != 3 {
		t.Errorf("probabilities = %#v", which.Probabilities)
	}
}

// TestEffectiveConfidenceLocalFormula 置信口径**本地算**（跨后端单源）。
//
// 判据数据取自真机实测（Laya 0.4.13，2026-10-23）：端点自算 confidence 与
// x_jev_confidence 不同，本地算出的值必须等于 Jev 口径（即等于 x_jev_confidence）。
func TestEffectiveConfidenceLocalFormula(t *testing.T) {
	// 真机 choice 响应：端点 confidence=0.9013（自有公式），x_jev_confidence=0.9712。
	choice := Answer{
		Type:           TypeChoice,
		Probabilities:  map[string]float64{"alpha": 0.9808, "beta": 0.0098, "gamma": 0.0094},
		Confidence:     ptrF(0.9013),
		XJevConfidence: ptrF(0.9712),
	}
	got, ok := choice.EffectiveConfidence()
	if !ok {
		t.Fatal("choice 有效置信度缺失")
	}
	if got < 0.9711 || got > 0.9713 {
		t.Errorf("choice 有效置信度 = %v, want 0.9712（(N·p_top−1)/(N−1)）", got)
	}
	if choice.XJevConfidence == nil || *choice.XJevConfidence != 0.9712 {
		t.Errorf("x_jev_confidence 未解析: %#v", choice.XJevConfidence)
	}
	// 真机 score 响应：4 档，m=1，MAD=1 ⇒ 1−0.8064=0.1936。
	score := Answer{
		Type:           TypeScore,
		Probabilities:  map[string]float64{"0": 0.2061, "1": 0.3927, "2": 0.2021, "3": 0.1991},
		Legend:         map[string]string{"0": "低", "1": "较低", "2": "中", "3": "高"},
		XJevConfidence: ptrF(0.1936),
	}
	got, ok = score.EffectiveConfidence()
	if !ok {
		t.Fatal("score 有效置信度缺失")
	}
	if got < 0.1935 || got > 0.1937 {
		t.Errorf("score 有效置信度 = %v, want 0.1936（Jev 档位距离公式）", got)
	}
	// noul：端点给 p=0.8125（Laya 口径），本地一律 |2p−1|=0.625（Jev 口径）。
	noul := Answer{Type: TypeNoul, Noul: ptrF(0.8125), Confidence: ptrF(0.8125)}
	got, ok = noul.EffectiveConfidence()
	if !ok {
		t.Fatal("noul 有效置信度缺失")
	}
	if got < 0.6249 || got > 0.6251 {
		t.Errorf("noul 有效置信度 = %v, want 0.625（|2p−1|，不吃端点自算值）", got)
	}
	// 分布缺失 ⇒ 回落端点 confidence。
	bare := Answer{Type: TypeChoice, Confidence: ptrF(0.7)}
	got, ok = bare.EffectiveConfidence()
	if !ok || got != 0.7 {
		t.Errorf("无分布时应回落端点 confidence: %v %v", got, ok)
	}
	// 两候选退化为 p_top−p_second。
	two := Answer{Type: TypeChoice, Probabilities: map[string]float64{"a": 0.78, "b": 0.22}}
	got, _ = two.EffectiveConfidence()
	if got < 0.5599 || got > 0.5601 {
		t.Errorf("两候选置信度 = %v, want 0.56", got)
	}
}

// TestDecideScoreLegendObject legend 真机是**对象**（官方文档样例写数组）——
// 数组形态解析会直接失败，故以真机为准。
func TestDecideScoreLegendObject(t *testing.T) {
	srv := fakeEndpoint(t, http.StatusOK,
		`{"level":{"type":"score","score":3.87,"confidence":0.42,"probabilities":{"0":0.02,"1":0.05,"2":0.13,"3":0.45,"4":0.35},"legend":{"0":"最低","1":"低","2":"中","3":"高","4":"最高"}}}`)
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL})
	res, err := c.Decide(context.Background(), Request{
		State:     "一段待判定文本",
		Questions: map[string]Question{"level": {Type: TypeScore, Instructions: "多严重？", Criteria: []string{"最低", "低", "中", "高", "最高"}}},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	a, _ := res.Answer("level")
	if len(a.Legend) != 5 || a.Legend["4"] != "最高" {
		t.Errorf("legend = %#v（真机形状=对象 索引→档位）", a.Legend)
	}
	if a.Score == nil || *a.Score != 3.87 {
		t.Errorf("score = %#v", a.Score)
	}
}

// TestDecideAPIErrors 端点非 2xx ⇒ APIError（带状态码与响应片段）；
// 调用方据此回落，不把端点故障吞成「没答案」。
func TestDecideAPIErrors(t *testing.T) {
	srv := fakeEndpoint(t, http.StatusTooManyRequests, "")
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL})
	_, err := c.Decide(context.Background(), Request{
		State:     "x",
		Questions: map[string]Question{"a": {Type: TypeNoul}},
	})
	if err == nil {
		t.Fatal("429 应返错")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型 = %T, want *APIError", err)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Errorf("status = %d", apiErr.Status)
	}
	if !strings.Contains(apiErr.Error(), "boom") {
		t.Errorf("错误文案应带响应片段: %q", apiErr.Error())
	}
}

// TestNewNotConfigured 未配置 ⇒ ErrNotConfigured（不是「配了但不生效」）。
func TestNewNotConfigured(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if _, err := New(Config{BaseURL: "   "}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("空白 base_url err = %v, want ErrNotConfigured", err)
	}
}

// TestDecideBadRequest 组题自检：空题集 / 非法题型 ⇒ 本地拒（不发请求）。
func TestDecideBadRequest(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1"})
	if _, err := c.Decide(context.Background(), Request{State: "x"}); err == nil {
		t.Error("空题集应返错")
	}
	_, err := c.Decide(context.Background(), Request{
		State:     "x",
		Questions: map[string]Question{"a": {Type: "bool"}},
	})
	if err == nil || !strings.Contains(err.Error(), "题型非法") {
		t.Errorf("err = %v, want 题型非法", err)
	}
}

// TestDecideTimeout 端点挂死 ⇒ 超时返错（回落由调用方做，本包不吞）。
func TestDecideTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Decide(ctx, Request{State: "x", Questions: map[string]Question{"a": {Type: TypeNoul}}}); err == nil {
		t.Fatal("超时应返错")
	}
}

// TestDecideUnreachable 端点不可达 ⇒ 返错。
func TestDecideUnreachable(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1", Timeout: 200 * time.Millisecond})
	if _, err := c.Decide(context.Background(), Request{State: "x", Questions: map[string]Question{"a": {Type: TypeNoul}}}); err == nil {
		t.Fatal("端点不可达应返错")
	}
}

// TestMockMode mock 模式：不发网络请求、本地合成答案（验接线，非验质量）。
func TestMockMode(t *testing.T) {
	t.Setenv(MockEnv, "1")
	// 端点故意指向不可达地址：mock 模式下不该发出任何请求。
	c, err := New(Config{BaseURL: "http://127.0.0.1:1", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Decide(context.Background(), Request{
		State: "任意",
		Questions: map[string]Question{
			"route": {Type: TypeChoice, Criteria: map[string]string{"fault-diagnosis": "", "other": ""}},
			"plan":  {Type: TypeNoul},
			"risk":  {Type: TypeScore, Criteria: []string{"低", "中", "高"}},
		},
	})
	if err != nil {
		t.Fatalf("mock 模式不应发网络请求: %v", err)
	}
	if res.Model != "mock" {
		t.Errorf("model = %q, want mock", res.Model)
	}
	if a, _ := res.Answer("route"); a.Choice != "fault-diagnosis" {
		t.Errorf("route 答案 = %q", a.Choice)
	}
	if a, _ := res.Answer("plan"); a.Noul == nil || *a.Noul != 0.95 {
		t.Errorf("plan 答案 = %#v", a)
	}
	if a, _ := res.Answer("risk"); a.Score == nil {
		t.Errorf("risk 答案 = %#v", a)
	}
}

// TestMockModeOff mock 关闭时仍走真实端点（不可达 ⇒ 返错）。
func TestMockModeOff(t *testing.T) {
	t.Setenv(MockEnv, "0")
	c, _ := New(Config{BaseURL: "http://127.0.0.1:1", Timeout: 500 * time.Millisecond})
	if _, err := c.Decide(context.Background(), Request{
		State:     "x",
		Questions: map[string]Question{"a": {Type: TypeNoul}},
	}); err == nil {
		t.Fatal("mock 关闭时不可达端点应返错")
	}
}

// ptrF 浮点指针助手。
func ptrF(v float64) *float64 { return &v }
