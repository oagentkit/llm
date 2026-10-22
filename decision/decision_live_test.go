//go:build decision_live

// 真机验收：打**真实**判定端点（自托管 Laya 或托管 Jev）。
//
// 为什么单独一个 build tag 的文件：默认门禁（go test ./...）不碰外网、也不需要
// 端点在位；但「协议对拍单测全绿」**证明不了链路通**——官方文档样例与真实实现可能
// 形状不同、语义口径不同（实测两处：legend 文档写数组真机是对象；两个后端的
// confidence 给法不一致）。真机验收必须是可复跑的一条命令，不是一次性 curl。
//
// 跑法：
//
//	# 1) 起自托管判定端点（Apache-2.0 开源，pip install "laya[serve]"）
//	LAYA_PRELOAD=1 laya-serve            # 默认 127.0.0.1:8000
//
//	# 2) 打真端点
//	BQ_DECISION_LIVE=1 BQ_DECISION_BASE_URL=http://127.0.0.1:8000 \
//	  go test -tags decision_live -v ./decision/
//
//	# 托管 Jev 同理（需 key）：
//	BQ_DECISION_LIVE=1 BQ_DECISION_BASE_URL=https://<host> BQ_DECISION_API_KEY=... \
//	  go test -tags decision_live -v ./decision/
package decision_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/oagentkit/llm/decision"
)

// TestLiveDecision 真机一次判定：三原语同回（一次前向 pass 答完所有题）。
func TestLiveDecision(t *testing.T) {
	if os.Getenv("BQ_DECISION_LIVE") != "1" {
		t.Skip("未设 BQ_DECISION_LIVE=1（真机验收需显式开启）")
	}
	base := os.Getenv("BQ_DECISION_BASE_URL")
	if base == "" {
		t.Fatal("BQ_DECISION_BASE_URL 未设置")
	}
	c, err := decision.New(decision.Config{
		BaseURL: base,
		APIKey:  os.Getenv("BQ_DECISION_API_KEY"),
		Model:   os.Getenv("BQ_DECISION_MODEL"),
		Timeout: 120 * time.Second, // 首次调用可能要加载检查点（冷启动）
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	res, err := c.Decide(ctx, decision.Request{
		State: "用户申请退款，已扣款但订单未发货。",
		Questions: map[string]decision.Question{
			"department": {
				Type:         decision.TypeChoice,
				Instructions: "该转交哪个部门处理？",
				Criteria: map[string]string{
					"billing":   "账单、支付、退款",
					"technical": "故障、异常、报错",
					"other":     "其它",
				},
			},
			"churn_risk": {
				Type:         decision.TypeNoul,
				Instructions: "用户是否表达了放弃/离开的意向？",
			},
			"urgency": {
				Type:         decision.TypeScore,
				Instructions: "有多紧急？",
				Criteria:     []string{"不紧急", "较急", "阻塞", "紧急"},
			},
		},
	})
	if err != nil {
		t.Fatalf("真机判定失败: %v", err)
	}
	t.Logf("model=%s usage=%+v", res.Model, res.Usage)
	for name, a := range res.Answers {
		conf, has := a.EffectiveConfidence()
		t.Logf("答案 %q: type=%s choice=%q noul=%v score=%v conf=%.3f(有=%v) probs=%v",
			name, a.Type, a.Choice, a.Noul, a.Score, conf, has, a.Probabilities)
	}
	dep, ok := res.Answer("department")
	if !ok || dep.Choice == "" {
		t.Fatalf("选择题未答: %+v", res.Answers)
	}
	if len(dep.Probabilities) == 0 {
		t.Errorf("选择题应带概率分布: %+v", dep)
	}
	if _, ok := res.Answer("churn_risk"); !ok {
		t.Errorf("是否题未答: %+v", res.Answers)
	}
	if _, ok := res.Answer("urgency"); !ok {
		t.Errorf("评分题未答: %+v", res.Answers)
	}
	// 口径交叉验证：端点若给了 Jev 口径字段（x_jev_confidence），本地算出的置信度
	// 必须与它一致（实测逐位相等）——这是本地口径公式的**外部基准**。
	for name, a := range res.Answers {
		if a.XJevConfidence == nil {
			continue
		}
		local, has := a.EffectiveConfidence()
		if !has {
			continue
		}
		if diff := local - *a.XJevConfidence; diff > 1e-3 || diff < -1e-3 {
			t.Errorf("题 %q 的本地口径 %v 与端点 Jev 口径 %v 不一致（差 %v）", name, local, *a.XJevConfidence, diff)
		}
	}
}
