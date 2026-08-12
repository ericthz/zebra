package agent

import (
	"fmt"
	"log"

	"github.com/ericthz/aidemo/provider"
	"github.com/ericthz/aidemo/tool"
)

type Agent struct {
	provider provider.Provider
	registry *tool.Registry
	maxTurns int
}

func NewAgent(prov provider.Provider, reg *tool.Registry, maxTurns int) *Agent {
	if maxTurns <= 0 {
		maxTurns = 5
	}
	return &Agent{
		provider: prov,
		registry: reg,
		maxTurns: maxTurns,
	}
}

func (a *Agent) Run(userInput string) (string, error) {
	messages := []provider.Message{
		{Role: "user", Content: userInput},
	}
	tools := a.registry.ToProviderTools()

	for turn := 0; turn < a.maxTurns; turn++ {
		log.Printf("🔄 第 %d 轮调用...\n", turn+1)
		respMsg, err := a.provider.Chat(messages, tools)
		if err != nil {
			return "", fmt.Errorf("请求失败: %v", err)
		}

		if len(respMsg.ToolCalls) == 0 {
			return respMsg.Content, nil
		}

		log.Printf("🔧 模型请求调用 %d 个工具\n", len(respMsg.ToolCalls))
		messages = append(messages, respMsg)

		for _, tc := range respMsg.ToolCalls {
			log.Printf("  → 调用：%s 参数：%s\n", tc.Function.Name, tc.Function.Arguments)

			args, err := tool.ParseArguments(tc.Function.Arguments)
			if err != nil {
				return "", fmt.Errorf("参数解析失败: %v", err)
			}

			result, err := a.registry.Execute(tc.Function.Name, args)
			if err != nil {
				result = fmt.Sprintf("工具执行错误: %v", err)
			}
			log.Printf("  → 结果：%s\n", result)

			messages = append(messages, provider.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}
	return "", fmt.Errorf("达到最大迭代次数 %d，对话未完成", a.maxTurns)
}
