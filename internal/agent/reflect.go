// 反思与自一致性（P40）：LLM 推理深度提升。
//
// 背景：单次生成可能不够准/不够好。成熟 Agent 会在回答后追加一道
// "质量工序"：
//   - Reflect（反思）：让模型批判性检查自己的回答并给出改进版；
//   - SelfConsistent（自一致性）：独立采样多个回答，再择优/投票，
//     降低单次采样的随机性（Self-Consistency 是 CoT 的重要伴侣）。
//
// 可靠性策略：反思/择优模型不可用或输出不合规时，**回退原回答**，
// 绝不把"更差"的结果交给用户（B9 容错 + 质量优先）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ericthz/zebra/internal/provider"
)

// reflectSchema 反思输出的 JSON Schema（P17 强约束复用）。
var reflectSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"revised": map[string]interface{}{"type": "string"},
		"reason":  map[string]interface{}{"type": "string"},
	},
	"required": []string{"revised"},
}

// consensusSchema 自一致性择优输出的 JSON Schema。
var consensusSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"answer": map[string]interface{}{"type": "string"},
	},
	"required": []string{"answer"},
}

// Reflect 对回答做一轮"批判-改进"反思，返回修订后的回答；
// 反思失败时原样返回（质量兜底）。
func (a *Agent) Reflect(ctx context.Context, question, answer string) (string, error) {
	prompt := fmt.Sprintf(`你是严格的回答评审员。请批判性检查下面的回答是否准确、完整、贴题，
并给出改进后的版本。只输出 JSON，不要输出其它内容：
{"revised":"改进后的回答","reason":"改进理由"}
问题：%s
原回答：%s`, question, answer)

	data, err := provider.StructuredChat(ctx, a.cfg.Router,
		[]provider.Message{{Role: "user", Content: prompt}}, reflectSchema)
	if err != nil {
		return answer, nil // 评审模型不可用 → 回退原回答
	}
	var out struct {
		Revised string `json:"revised"`
	}
	if err := json.Unmarshal(data, &out); err != nil || strings.TrimSpace(out.Revised) == "" {
		return answer, nil
	}
	return out.Revised, nil
}

// SelfConsistent 自一致性：独立采样 samples 个回答，再用模型择优选出
// 最一致/最优的一个；采样或择优失败时回退首个可用回答。
func (a *Agent) SelfConsistent(ctx context.Context, question string, samples int) (string, error) {
	if samples <= 0 {
		samples = 3
	}
	// 只采一个样本就没有"一致性"可言，直接单次回答
	if samples == 1 {
		msg, _, err := a.cfg.Router.ChatWithFallback(ctx,
			[]provider.Message{{Role: "user", Content: question}}, nil)
		if err != nil {
			return "", err
		}
		return msg.Content, nil
	}

	var answers []string
	for i := 0; i < samples; i++ {
		msg, _, err := a.cfg.Router.ChatWithFallback(ctx, []provider.Message{{
			Role:    "user",
			Content: fmt.Sprintf("请独立、简洁地回答下面的问题（不要输出思考过程）：\n%s", question),
		}}, nil)
		if err != nil {
			continue
		}
		if msg.Content != "" {
			answers = append(answers, msg.Content)
		}
	}
	if len(answers) == 0 {
		return "", fmt.Errorf("自一致性采样全部失败")
	}
	if len(answers) == 1 {
		return answers[0], nil
	}

	// 择优：把候选交给模型选最佳（结构化输出，失败回退第一个候选）
	var b strings.Builder
	b.WriteString("下面是同一个问题的多个候选回答。请选出最准确、完整、一致的一个，只输出 JSON：\n")
	b.WriteString("{\"answer\":\"选中的回答\"}\n问题：")
	b.WriteString(question)
	b.WriteString("\n候选：\n")
	for i, a := range answers {
		fmt.Fprintf(&b, "[%d] %s\n", i+1, a)
	}
	data, err := provider.StructuredChat(ctx, a.cfg.Router,
		[]provider.Message{{Role: "user", Content: b.String()}}, consensusSchema)
	if err != nil {
		return answers[0], nil
	}
	var out struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(data, &out); err != nil || strings.TrimSpace(out.Answer) == "" {
		return answers[0], nil
	}
	return out.Answer, nil
}
