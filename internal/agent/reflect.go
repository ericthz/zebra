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

// RunReflect 一轮"先回答、再反思改进"的完整执行（P40），供 server/CLI 的
// reflect 模式统一调用。
//
// 与"Run + Reflect 两步拼装"的区别（P2-B）：
//   - 修订版是【最终交付物】，必须再次过 D18 输出审核（原回答在 Run 内已审，
//     修订版是新的输出，须单独审核）；
//   - 修订版要【写回会话历史】——Run 已把"原回答"写入历史，这里用
//     replaceLastAssistant 原地替换，保证下一轮上下文读到的是改进后的回答，
//     而不是旧版本（旧版本写入记忆的残留通过 replaceLastAssistant 一并修正）。
//
// 修订版审核失败时返回错误，历史里保留已通过审核的原回答（不把违规文本入库）。
func (a *Agent) RunReflect(ctx context.Context, question string, opts RunOptions) (string, error) {
	ctx = a.usageCtx(ctx) // F-3：反思调用计入用量
	answer, err := a.Run(ctx, question, opts)
	if err != nil {
		return "", err
	}
	revised, err := a.Reflect(ctx, question, answer)
	if err != nil || strings.TrimSpace(revised) == "" || revised == answer {
		return answer, nil // 反思失败/未改进 → 回退原回答（质量兜底）
	}
	// 修订版是最终交付物，必须单独过 D18 输出审核（P2-B）。
	if err := a.checkOutput(revised); err != nil {
		return "", err
	}
	// 写回历史：替换 Run 写入的原回答，下一轮上下文读到修订版。
	a.replaceLastAssistant(revised)
	// F-4：缓存与记忆里的原回答也一并替换，保证"再问同一问题"（缓存命中）
	// 与记忆召回返回的是改进版，而不是质量更差的旧版本。
	if a.cfg.Cache != nil {
		a.cfg.Cache.Put(ctx, a.user, question, revised)
	}
	if a.cfg.Mem != nil {
		a.cfg.Mem.ReplaceLast(a.sessionID, question, revised)
	}
	return revised, nil
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
//
// 注意（六10）：本方法直连 Router 采样，不走 run() 的 rememberTurn，
// 因此必须在返回前手动 rememberTurn，否则"自一致性"轮次不进会话历史
// 与分层记忆，下一轮对话读不到上一轮（consistent 模式多轮上下文断裂）。
func (a *Agent) SelfConsistent(ctx context.Context, question string, samples int, images ...string) (string, error) {
	ctx = a.usageCtx(ctx) // F-3：采样/择优全部计入用量
	if samples <= 0 {
		samples = 3
	}
	// D18 输入审核（P2-A）：先于采样与择优 LLM 调用。
	if err := a.checkInput(question); err != nil {
		return "", err
	}
	// 只采一个样本就没有"一致性"可言，直接单次回答
	if samples == 1 {
		msg, _, err := a.cfg.Router.ChatWithFallback(ctx,
			[]provider.Message{a.userMessage(question, images)}, nil)
		if err != nil {
			return "", err
		}
		// D18 输出审核（P2-A）：先审核再持久化。
		if err := a.checkOutput(msg.Content); err != nil {
			return "", err
		}
		a.rememberTurn(question, msg.Content)
		return msg.Content, nil
	}

	var answers []string
	for i := 0; i < samples; i++ {
		msg, _, err := a.cfg.Router.ChatWithFallback(ctx, []provider.Message{a.userMessage(
			fmt.Sprintf("请独立、简洁地回答下面的问题（不要输出思考过程）：\n%s", question), images)}, nil)
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
		if err := a.checkOutput(answers[0]); err != nil {
			return "", err
		}
		a.rememberTurn(question, answers[0])
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
		if cerr := a.checkOutput(answers[0]); cerr != nil {
			return "", cerr
		}
		a.rememberTurn(question, answers[0])
		return answers[0], nil
	}
	var out struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(data, &out); err != nil || strings.TrimSpace(out.Answer) == "" {
		if cerr := a.checkOutput(answers[0]); cerr != nil {
			return "", cerr
		}
		a.rememberTurn(question, answers[0])
		return answers[0], nil
	}
	if err := a.checkOutput(out.Answer); err != nil {
		return "", err
	}
	a.rememberTurn(question, out.Answer)
	return out.Answer, nil
}
