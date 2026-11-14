// Package fitkit 窗口拟合的共享内核单源（llm#201）：root fitInputFor 与
// wrap FitModel 各自实现窗口治理，此前的共享语义——错误/提醒结果保护判定、
// 工具轮组划分——两边各持一份，判据一侧调整时另一侧静默漂移。本包收拢
// 两者，依赖面只有 eino schema（wrap 的零根包依赖约束不受影响：internal
// 包仅本模块可见，且不反向依赖 root）。
package fitkit

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

// KeepRecentGroups microcompact 保留最近 N 个工具轮组（wrap L1 与 root L4
// 共用）：越近的轮越可能被再次引用，丢弃收益随时间衰减。
const KeepRecentGroups = 5

// GroupDropNote 整组丢弃注记（root L4 / wrap L4 共用文案，审计按它 grep）：
// 替换被丢弃的「assistant(带 ToolCalls) + 其配对结果」轮组。
const GroupDropNote = "[早期工具调用轮已整体丢弃以适配上下文窗口：microcompact-group]"

// GroupDropMarker 注记的识别标记（CoalesceDropNotes 按它找相邻注记）。
const GroupDropMarker = "microcompact-group"

// GroupNoteRunes 组丢弃注记的 rune 长度（L4 的「丢弃后必须变小」守卫基准——
// 组合计不大于注记长度的轮组丢了白丢）。
var GroupNoteRunes = utf8.RuneCountInString(GroupDropNote)

// CoalesceDropNotes 把相邻的整组丢弃注记合并为一条汇总注记，返回收短后的
// 消息列与节省的 rune 数（0 = 无相邻注记可并）。注记自身占位不是免费的：
// 丢弃轮多时注记合计可能吃满剩余预算（15×41 rune ≈ 一轮中参数），合并是
// 纯汇总——只影响注记形态，不触碰任何工具轮，保护语义不变。
func CoalesceDropNotes(msgs []*schema.Message) ([]*schema.Message, int) {
	type seg struct{ start, count int }
	var runs []seg
	for i, m := range msgs {
		if m == nil || !strings.Contains(m.Content, GroupDropMarker) {
			continue
		}
		if n := len(runs); n > 0 && runs[n-1].start+runs[n-1].count == i {
			runs[n-1].count++
		} else {
			runs = append(runs, seg{i, 1})
		}
	}
	if len(runs) == 0 {
		return msgs, 0
	}
	out := make([]*schema.Message, 0, len(msgs))
	saved := 0
	for i := 0; i < len(msgs); {
		merged := false
		for _, s := range runs {
			if s.start != i || s.count < 2 {
				continue
			}
			note := &schema.Message{Role: schema.Assistant,
				Content: fmt.Sprintf("[早期 %d 轮工具调用已整体丢弃以适配上下文窗口：%s]", s.count, GroupDropMarker)}
			for k := range s.count {
				saved += utf8.RuneCountInString(msgs[i+k].Content)
			}
			saved -= utf8.RuneCountInString(note.Content)
			out = append(out, note)
			i += s.count
			merged = true
			break
		}
		if !merged {
			out = append(out, msgs[i])
			i++
		}
	}
	if saved <= 0 {
		return msgs, 0
	}
	return out, saved
}

// IsProtectedResult 错误/提醒结果保护（compact「出错结果默认保留」同款
// 语义）：携带提醒信封（含 tool-error 标记）的结果是模型自纠与规则遵循的
// 依据，窗口裁剪/清理不碰。
func IsProtectedResult(content string) bool {
	return strings.Contains(content, "<system-reminder>") || strings.Contains(content, "【tool-error】")
}

// ToolGroups 划分工具结果轮次组：一个 assistant（带 ToolCalls）开启一轮，
// 其后连续的 Tool 消息属该组。返回每组的消息下标切片（按出现序）。孤立
// 工具结果（无所属 assistant 轮）不入组——保守不清理。
func ToolGroups(msgs []*schema.Message) (groups [][]int) {
	for i := 0; i < len(msgs); {
		if msgs[i].Role == schema.Assistant && len(msgs[i].ToolCalls) > 0 {
			start := i
			i++
			var g []int
			for i < len(msgs) && msgs[i].Role == schema.Tool {
				g = append(g, i)
				i++
			}
			g = append([]int{start}, g...) // assistant 本体入组首位（组丢弃连它一起）
			if len(g) > 1 {
				groups = append(groups, g)
			}
			continue
		}
		i++
	}
	return groups
}

// GroupProtected 报告轮组是否含保护内容（任一成员携带提醒信封/tool-error
// 即保护整组——模型自纠依据不丢）。
func GroupProtected(msgs []*schema.Message, group []int) bool {
	for _, i := range group {
		if msgs[i] != nil && IsProtectedResult(msgs[i].Content) {
			return true
		}
	}
	return false
}
