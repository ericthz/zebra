// 多 Agent 辩论（P46）：让两个不同立场的"辩手"先独立作答，再互相看到
// 对方观点后给出最终立场，最后由评审模型选优——提升答案的全面性与稳健性。
//
// 流程：
//
//	左/右独立回答 → 交换观点（各给一轮反驳/完善）→ 评审选优（结构化输出）
//
// 可靠性：评审失败时回退左方最终立场（不阻塞，B9 容错）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ericthz/zebra/internal/provider"
)

// debateSchema 辩论评审输出 JSON Schema。
var debateSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"answer": map[string]interface{}{"type": "string"},
		"winner": map[string]interface{}{"type": "string", "enum": []string{"left", "right", "tie"}},
		"reason": map[string]interface{}{"type": "string"},
	},
	"required": []string{"answer"},
}

// Debate 双 Agent 辩论。返回（胜出答案, 评审理由, error）。
// Debate 双 Agent 辩论（P46）：左右立场独立作答→交换观点→评审选优。
// images 为可选多模态图片（C14，可省略；有图时两路辩手都能看到）。
func (a *Agent) Debate(ctx context.Context, question, leftPersona, rightPersona string, images ...string) (string, string, error) {
	ctx = a.usageCtx(ctx) // F-3：辩论 5 次 LLM 调用全部计入用量
	if leftPersona == "" {
		leftPersona = "你是左方辩手：严谨，偏好引用事实、数据与计算验证。"
	}
	if rightPersona == "" {
		rightPersona = "你是右方辩手：务实，偏好简明、直接、可执行的结论。"
	}

	// D18 输入审核（P2-A）：先于两路辩手 LLM 调用。
	if err := a.checkInput(question); err != nil {
		return "", "", err
	}

	// 1. 独立首轮回答
	left1, err := a.chatPersona(ctx, leftPersona, question, images)
	if err != nil {
		return "", "", err
	}
	right1, err := a.chatPersona(ctx, rightPersona, question, images)
	if err != nil {
		return "", "", err
	}

	// 2. 交换观点后的最终立场
	left2, err := a.chatPersona(ctx, leftPersona,
		fmt.Sprintf("对方观点：%s\n请结合对方观点，给出你的最终立场。\n问题：%s", right1, question), images)
	if err != nil {
		return "", "", err
	}
	right2, err := a.chatPersona(ctx, rightPersona,
		fmt.Sprintf("对方观点：%s\n请结合对方观点，给出你的最终立场。\n问题：%s", left1, question), images)
	if err != nil {
		return "", "", err
	}

	// 3. 评审选优（结构化输出；失败回退左方最终立场）
	prompt := fmt.Sprintf(`你是辩论评审。请从下面两个最终立场中选出更优的一个，只输出 JSON：
{"answer":"选中的回答","winner":"left|right|tie","reason":"理由"}
问题：%s
左方最终立场：%s
右方最终立场：%s`, question, left2, right2)
	data, err := provider.StructuredChat(ctx, a.cfg.Router,
		[]provider.Message{{Role: "user", Content: prompt}}, debateSchema)
	if err != nil {
		// D18 输出审核（P2-A）：回退立场也是最终交付物，须先审核再持久化。
		if cerr := a.checkOutput(left2); cerr != nil {
			return "", "", cerr
		}
		a.rememberTurn(question, left2)
		return left2, "评审失败，回退左方立场", nil
	}
	var out struct {
		Answer string `json:"answer"`
		Winner string `json:"winner"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Answer == "" {
		if cerr := a.checkOutput(left2); cerr != nil {
			return "", "", cerr
		}
		a.rememberTurn(question, left2)
		return left2, "评审输出异常，回退左方立场", nil
	}
	if err := a.checkOutput(out.Answer); err != nil {
		return "", "", err
	}
	a.rememberTurn(question, out.Answer)
	return out.Answer, out.Reason, nil
}

// chatPersona 用 persona 作为系统提示执行一轮对话。
func (a *Agent) chatPersona(ctx context.Context, persona, question string, images []string) (string, error) {
	msg, _, err := a.cfg.Router.ChatWithFallback(ctx, []provider.Message{
		{Role: "system", Content: persona},
		a.userMessage(question, images),
	}, nil)
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}
