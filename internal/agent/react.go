// ReAct 轨迹（P45）：把 Agent 执行从"隐式工具循环"升级为"显式推理-行动"。
//
// ReAct（Reasoning + Acting）是最经典的 Agent 范式：每一步模型输出
//
//	{"thought": 推理, "action": {工具名/参数}, "answer": 最终答案}
//
// 有行动就执行工具并把观察（observation）回填，直到模型给出答案——
// 推理轨迹显式化，既便于调试，也提升复杂任务的成功率。
//
// 可靠性：工具执行走既有 registry（权限/审计/高危确认全链路）；
// 达到最大步数或模型输出异常时报错，不静默。
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ericthz/zebra/internal/provider"
)

// reactStepSchema ReAct 每步输出 JSON Schema（P17 强约束）。
var reactStepSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"thought": map[string]interface{}{"type": "string"},
		"action": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string"},
				"args": map[string]interface{}{"type": "object"},
			},
		},
		"answer": map[string]interface{}{"type": "string"},
	},
}

// ReAct 运行 ReAct 推理-行动循环，返回最终答案。
// maxSteps<=0 时默认 6 步；工具执行复用 registry（权限/审计/高危确认）。
func (a *Agent) ReAct(ctx context.Context, question string, opts RunOptions, maxSteps int) (string, error) {
	if maxSteps <= 0 {
		maxSteps = 6
	}
	msgs := []provider.Message{
		{Role: "system", Content: `你是 ReAct Agent。每一步只输出 JSON：
{"thought":"你的推理","action":{"name":"工具名","args":{}},"answer":""}
需要调用工具时填 action 并置空 answer；完成任务时填 answer 并置空 action。`},
		{Role: "user", Content: question},
	}

	for step := 0; step < maxSteps; step++ {
		data, err := provider.StructuredChat(ctx, a.cfg.Router, msgs, reactStepSchema)
		if err != nil {
			return "", fmt.Errorf("ReAct 第 %d 步推理失败: %w", step+1, err)
		}
		var out struct {
			Thought string `json:"thought"`
			Action  struct {
				Name string                 `json:"name"`
				Args map[string]interface{} `json:"args"`
			} `json:"action"`
			Answer string `json:"answer"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", fmt.Errorf("ReAct 输出解析失败: %w", err)
		}
		msgs = append(msgs, provider.Message{Role: "assistant", Content: string(data)})

		// 无行动 → 输出答案（或异常）
		if out.Action.Name == "" {
			if out.Answer != "" {
				return out.Answer, nil
			}
			return "", fmt.Errorf("ReAct 第 %d 步既无行动也无答案", step+1)
		}

		// 执行工具（权限/审计/高危确认全链路复用 toolLoop 同款路径）
		result, terr := a.cfg.Tools.Execute(ctx, out.Action.Name, out.Action.Args, a.user, a.role, opts.Confirm != nil)
		if terr != nil {
			result = "工具执行错误: " + terr.Error()
		}
		if a.cfg.OnTool != nil {
			a.cfg.OnTool(out.Action.Name, out.Action.Args, terr == nil, terr)
		}
		msgs = append(msgs, provider.Message{Role: "user", Content: "【观察】工具结果：" + result})
	}
	return "", fmt.Errorf("ReAct 达到最大步数 %d，未收敛", maxSteps)
}
