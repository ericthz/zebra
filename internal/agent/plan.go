// 规划-执行编排（P10，Plan-then-Execute）。
//
// 背景：单次工具循环适合"一步到位"的任务；但成熟 Agent 面对复杂任务
// （"做一个季度分析报告"）需要先【拆解】再【逐个执行】——
// 这正是多阶段编排的意义。
//
// 三阶段：
//
//	阶段1 规划(plan)：让 LLM 把大任务拆成有序子步骤（JSON）
//	阶段2 执行(executeStep)：逐个执行子任务（复用 toolLoop，可带工具调用）
//	阶段3 汇总：把各步骤结果拼成最终回答
//
// 设计要点：
//   - 子步骤执行【不写】会话历史与记忆（toolLoop 不碰 history/mem），
//     避免中间过程污染最终对话（只有结果进入汇总）
//   - 复用 Agent 已有能力（router/工具/上下文工程），无新依赖
//
// 生产演化方向：
//   - 步骤间依赖 → 引入 DAG/工作流状态机（LangGraph 式）
//   - 步骤可并行（无依赖步骤 fan-out）
//   - 失败步骤自动重试或降级、人工介入（HITL）
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ericthz/zebra/internal/provider"
)

// PlanStep 规划出的一个子步骤。
type PlanStep struct {
	Title string `json:"title"` // 步骤标题（供人阅读）
	Task  string `json:"task"`  // 给执行器的子任务描述
}

// Plan 规划结果。
type Plan struct {
	Summary string     `json:"summary"`
	Steps   []PlanStep `json:"steps"`
}

// PlanAndExecute 规划-执行两阶段编排入口。
func (a *Agent) PlanAndExecute(ctx context.Context, userInput string, opts RunOptions) (string, error) {
	// 阶段 1：规划
	plan, err := a.plan(ctx, userInput)
	if err != nil {
		return "", fmt.Errorf("规划失败: %w", err)
	}
	if len(plan.Steps) == 0 {
		// 模型判断无需拆分 → 退化为普通执行
		return a.Run(ctx, userInput, opts)
	}

	// 阶段 2：逐步执行
	var parts []string
	for i, step := range plan.Steps {
		out, serr := a.executeStep(ctx, step, opts)
		if serr != nil {
			out = fmt.Sprintf("（步骤执行失败: %v）", serr)
		}
		parts = append(parts, fmt.Sprintf("步骤%d【%s】: %s", i+1, step.Title, out))
	}

	// 阶段 3：汇总
	return fmt.Sprintf("按规划完成（共 %d 步）：\n%s", len(plan.Steps), strings.Join(parts, "\n")), nil
}

// plan 阶段 1：让 LLM 输出 JSON 步骤列表。
// P17 结构化输出强约束：用 StructuredChat（优先 response_format 强约束，
// 回退普通调用 + schema 校验），保证拿到合法规划 JSON。
func (a *Agent) plan(ctx context.Context, userInput string) (*Plan, error) {
	prompt := fmt.Sprintf(`你是一个任务规划器。请把下面的用户请求拆解为 2~5 个有序的执行步骤。
只输出 JSON，不要其它内容：
{"summary":"一句话总结计划","steps":[{"title":"步骤标题","task":"给执行器的具体子任务描述"}]}
用户请求：%s`, userInput)

	data, err := provider.StructuredChat(ctx, a.cfg.Router, []provider.Message{
		{Role: "user", Content: prompt},
	}, planSchema)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// planSchema 规划输出的 JSON Schema（P17 强约束）。
var planSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"summary": map[string]interface{}{"type": "string"},
		"steps": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{"type": "string"},
					"task":  map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"title", "task"},
			},
		},
	},
	"required": []interface{}{"summary", "steps"},
}

// parsePlan 从模型回复中稳健抽取并解析规划 JSON。
func parsePlan(content string) (*Plan, error) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("模型未返回规划 JSON")
	}
	var p Plan
	if err := json.Unmarshal([]byte(content[start:end+1]), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// executeStep 阶段 2：执行单个子任务（一次工具循环）。
// 关键：不写历史/记忆，只返回该步骤的最终文本。
func (a *Agent) executeStep(ctx context.Context, step PlanStep, opts RunOptions) (string, error) {
	msgs := []provider.Message{}
	if sys, err := a.cfg.Prompts.Render(a.cfg.PromptName, map[string]string{"role": a.role}); err == nil {
		msgs = append(msgs, provider.Message{Role: "system", Content: sys})
	}
	msgs = append(msgs, provider.Message{Role: "user", Content: step.Task})
	if a.cfg.Window != nil {
		msgs = a.cfg.Window.Trim(msgs)
	}

	final, lastErr, _ := a.toolLoop(ctx, msgs, a.cfg.Tools.ToolsFor(a.role), opts, nil)
	if lastErr != nil && final == "" {
		return "", lastErr
	}
	return final, nil
}
