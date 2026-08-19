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
	Title     string `json:"title"`      // 步骤标题（供人阅读）
	StepTitle string `json:"step_title"` // 小模型常用别名，归一化到 Title
	Task      string `json:"task"`       // 给执行器的子任务描述
}

// Plan 规划结果。
type Plan struct {
	Summary string     `json:"summary"`
	Title   string     `json:"title"` // 小模型常用 title 代替 summary，归一化到 Summary
	Steps   []PlanStep `json:"steps"`
}

// PlanAndExecute 规划-执行两阶段编排入口。
func (a *Agent) PlanAndExecute(ctx context.Context, userInput string, opts RunOptions) (string, error) {
	ctx = a.usageCtx(ctx) // F-3：规划/子步骤执行计入用量
	// D18 输入审核（P2-A）：先于规划 LLM 调用与子步骤执行。
	if err := a.checkInput(userInput); err != nil {
		return "", err
	}
	// 阶段 1：规划
	plan, err := a.plan(ctx, userInput, opts.Images)
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
		out, serr := a.executeStep(ctx, step, opts, nil)
		if serr != nil {
			out = fmt.Sprintf("（步骤执行失败: %v）", serr)
		}
		parts = append(parts, fmt.Sprintf("步骤%d【%s】: %s", i+1, step.Title, out))
	}

	// 阶段 3：汇总（把"问题→最终汇总"写入历史与记忆，保证多轮上下文连续）
	summary := fmt.Sprintf("按规划完成（共 %d 步）：\n%s", len(plan.Steps), strings.Join(parts, "\n"))
	// D18 输出审核（P2-A）：先审核再持久化，违规汇总不进历史/记忆。
	if err := a.checkOutput(summary); err != nil {
		return "", err
	}
	a.rememberTurn(userInput, summary)
	return summary, nil
}

// PlanAndExecuteStream 规划-执行的流式版：执行过程以 phase/tool_call 事件
// 推送给前端（规划中 → 执行步骤 i/N → 汇总），最终答案以 delta 输出。
func (a *Agent) PlanAndExecuteStream(ctx context.Context, userInput string, opts RunOptions, emit func(Event)) (string, error) {
	if emit == nil {
		return a.PlanAndExecute(ctx, userInput, opts)
	}
	ctx = a.usageCtx(ctx) // F-3：流式规划-执行计入用量
	// D18 输入审核（P2-A）：与 run 同一道横切点。
	if err := a.checkInput(userInput); err != nil {
		return "", err
	}
	emit(Event{Type: EventPhase, Phase: "规划中…"})
	plan, err := a.plan(ctx, userInput, opts.Images)
	if err != nil {
		return "", fmt.Errorf("规划失败: %w", err)
	}
	if len(plan.Steps) == 0 {
		emit(Event{Type: EventPhase, Phase: "无需拆解，直接执行"})
		return a.run(ctx, userInput, opts, emit)
	}
	emit(Event{Type: EventPhase, Phase: fmt.Sprintf("已规划：%s（共 %d 步）", plan.Summary, len(plan.Steps))})

	var parts []string
	for i, step := range plan.Steps {
		emit(Event{Type: EventPhase, Phase: fmt.Sprintf("执行步骤 %d/%d：%s", i+1, len(plan.Steps), step.Title)})
		out, serr := a.executeStep(ctx, step, opts, emit)
		if serr != nil {
			out = fmt.Sprintf("（步骤执行失败: %v）", serr)
		}
		parts = append(parts, fmt.Sprintf("步骤%d【%s】: %s", i+1, step.Title, out))
	}
	summary := fmt.Sprintf("按规划完成（共 %d 步）：\n%s", len(plan.Steps), strings.Join(parts, "\n"))
	// D18 输出审核（P2-A）：先审核再持久化。
	if err := a.checkOutput(summary); err != nil {
		return "", err
	}
	emit(Event{Type: EventPhase, Phase: "汇总完成"})
	emit(Event{Type: EventDelta, Content: summary})
	a.rememberTurn(userInput, summary)
	return summary, nil
}

// plan 阶段 1：让 LLM 输出 JSON 步骤列表。
// P17 结构化输出强约束：用 StructuredChat（优先 response_format 强约束，
// 回退普通调用 + schema 校验），保证拿到合法规划 JSON。
func (a *Agent) plan(ctx context.Context, userInput string, images []string) (*Plan, error) {
	prompt := fmt.Sprintf(`你是一个任务规划器。请把下面的用户请求拆解为 2~5 个有序的执行步骤。
只输出 JSON，不要其它内容：
{"summary":"一句话总结计划","steps":[{"title":"步骤标题","task":"给执行器的具体子任务描述"}]}
用户请求：%s`, userInput)

	data, err := provider.StructuredChat(ctx, a.cfg.Router, []provider.Message{
		a.userMessage(prompt, images),
	}, planSchema)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	// 归一化小模型的不规范字段（title→summary，step_title→title）
	if p.Summary == "" {
		p.Summary = p.Title
	}
	for i := range p.Steps {
		if p.Steps[i].Title == "" {
			p.Steps[i].Title = p.Steps[i].StepTitle
		}
	}
	return &p, nil
}

// planSchema 规划输出的 JSON Schema（P17 强约束）。
var planSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"summary": map[string]interface{}{"type": "string"},
		"title":   map[string]interface{}{"type": "string"}, // 小模型别名
		"steps": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title":      map[string]interface{}{"type": "string"},
					"step_title": map[string]interface{}{"type": "string"}, // 小模型别名
					"task":       map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"task"},
			},
		},
	},
	"required": []interface{}{"steps"},
}

// executeStep 阶段 2：执行单个子任务（一次工具循环）。
// 关键：不写历史/记忆，只返回该步骤的最终文本。
// 多模态（C14）：把 opts.Images 一并带给子步骤，让"分析这张图"类子任务
// 能真正看到图（否则子步骤只见文字任务描述，仍是半实现）。
func (a *Agent) executeStep(ctx context.Context, step PlanStep, opts RunOptions, emit func(Event)) (string, error) {
	msgs := []provider.Message{}
	if sys, err := a.cfg.Prompts.Render(a.cfg.PromptName, map[string]string{"role": a.role}); err == nil {
		msgs = append(msgs, provider.Message{Role: "system", Content: sys})
	}
	msgs = append(msgs, a.userMessage(step.Task, opts.Images))
	if a.cfg.Window != nil {
		msgs = a.cfg.Window.Trim(ctx, msgs)
	}

	final, lastErr, _ := a.toolLoop(ctx, msgs, a.cfg.Tools.ToolsFor(a.role), opts, emit)
	if lastErr != nil && final == "" {
		return "", lastErr
	}
	return final, nil
}
