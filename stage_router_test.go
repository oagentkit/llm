package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeGen 可脚本化 Generator（记录收到的 stage）。
type fakeGen struct {
	name   string
	calls  *[]string
	budget TokenAccountant
}

func (f *fakeGen) Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error) {
	*f.calls = append(*f.calls, f.name+"|"+stage)
	return &schema.Message{Content: f.name}, nil
}
func (f *fakeGen) GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error {
	*f.calls = append(*f.calls, f.name+"|json|"+stage)
	return nil
}
func (f *fakeGen) RawModel() model.BaseChatModel { return nil }
func (f *fakeGen) UsedTokens() int {
	if f.budget != nil {
		return f.budget.Used()
	}
	return 0
}
func (f *fakeGen) BoundBudget() TokenAccountant { return f.budget }

func TestStageRouterExactWinsOverPrefix(t *testing.T) {
	calls := []string{}
	def := &fakeGen{name: "default", calls: &calls}
	r1 := &fakeGen{name: "r1", calls: &calls}
	r1a := &fakeGen{name: "r1a", calls: &calls}
	qa := &fakeGen{name: "qa", calls: &calls}

	sr := NewStageRouter(def)
	sr.Use("R1", r1)
	sr.Use("R1a", r1a)
	sr.Use("qa", qa)

	msgs := []*schema.Message{schema.UserMessage("x")}
	for _, stage := range []string{"R1", "R1a", "R2", "qa"} {
		if _, err := sr.Generate(context.Background(), stage, msgs); err != nil {
			t.Fatal(err)
		}
	}
	// 精确优先：R1a 走精确路由而非 R1 前缀
	if calls[0] != "r1|R1" || calls[1] != "r1a|R1a" || calls[2] != "default|R2" || calls[3] != "qa|qa" {
		t.Fatalf("路由结果不符: %v", calls)
	}
	// 最长前缀：注册 R1 与 R1 后再注册 "plugin:"，"plugin:mcp" 应命中前缀
	sr.Use("plugin:", &fakeGen{name: "reviewer", calls: &calls})
	if _, err := sr.Generate(context.Background(), "plugin:ocr", msgs); err != nil {
		t.Fatal(err)
	}
	if calls[len(calls)-1] != "reviewer|plugin:ocr" {
		t.Fatalf("plugin: 前缀应命中: %v", calls[len(calls)-1])
	}
}

func TestStageRouterUsedTokensAggregates(t *testing.T) {
	calls := []string{}
	b1, b2 := NewBudget(100), NewBudget(50)
	b1.Add(30)
	b2.Add(20)
	sr := NewStageRouter(&fakeGen{name: "def", calls: &calls, budget: b1})
	sr.Use("R3", &fakeGen{name: "r3", calls: &calls, budget: b2})
	if got := sr.UsedTokens(); got != 50 {
		t.Fatalf("UsedTokens 应聚合全部链: %d", got)
	}
}

func TestStageRouterUsedTokensSharedBudgetDedup(t *testing.T) {
	// 任务预算口径：各链共享同一 Budget，UsedTokens 必须按预算身份去重
	// （逐链求和会得到 B×链数，夸大实际消耗）
	calls := []string{}
	b := NewBudget(1000)
	b.Add(120)
	sr := NewStageRouter(&fakeGen{name: "def", calls: &calls, budget: b})
	sr.Use("R1", &fakeGen{name: "r1", calls: &calls, budget: b})
	sr.Use("R3", &fakeGen{name: "r3", calls: &calls, budget: b})
	if got := sr.UsedTokens(); got != 120 {
		t.Fatalf("共享预算应去重: got %d, want 120", got)
	}
}

// promLikeAccountant 模拟 Prometheus 风格自定义记账器：**值类型**实现 +
// []string label 字段——动态类型不可比，旧实现以接口值做 map 键直接 panic（#81）。
type promLikeAccountant struct {
	labels []string
	used   int
}

func (p promLikeAccountant) Add(int)        {} // 值接收者：本测试只读 Used，Add 空实现仅凑接口
func (p promLikeAccountant) Used() int      { return p.used }
func (p promLikeAccountant) Remaining() int { return 0 }

func TestStageRouterUsedTokensIncomparableAccountant(t *testing.T) {
	// #81：不可比值类型 TokenAccountant 经 BoundBudget 进路由——
	// 修前 UsedTokens 在 seen[acc] 处 panic，修后不 panic 且去重正确。
	calls := []string{}
	acc := promLikeAccountant{labels: []string{"task", "planning"}, used: 40}
	sr := NewStageRouter(&fakeGen{name: "def", calls: &calls, budget: acc})
	sr.Use("R1", &fakeGen{name: "r1", calls: &calls, budget: acc})
	sr.Use("R3", &fakeGen{name: "r3", calls: &calls, budget: acc})
	// 同一记账器（等值）注入多条链：内容身份去重，只计一次
	if got := sr.UsedTokens(); got != 40 {
		t.Fatalf("不可比记账器应去重只计一次: got %d, want 40", got)
	}
	// 不同内容的值记账器互不去重，各计一次
	other := promLikeAccountant{labels: []string{"task", "review"}, used: 15}
	sr.Use("R4", &fakeGen{name: "r4", calls: &calls, budget: other})
	if got := sr.UsedTokens(); got != 55 {
		t.Fatalf("不同值记账器应各计一次: got %d, want 55", got)
	}
}

func TestStageRouterJSONAndNilSafety(t *testing.T) {
	calls := []string{}
	sr := NewStageRouter(&fakeGen{name: "def", calls: &calls})
	var out struct{}
	if err := sr.GenerateJSON(context.Background(), "R2", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls[0], "json|R2") {
		t.Fatalf("JSON 应走缺省链: %v", calls)
	}
	if sr.RawModel() != nil {
		t.Fatal("RawModel 透传缺省链")
	}
}
