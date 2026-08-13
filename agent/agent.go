// agent/agent.go
package agent

import (
	"fmt"
	"log"
	"strings"

	"github.com/ericthz/zebra/memory"
	"github.com/ericthz/zebra/provider"
	"github.com/ericthz/zebra/tool"
)

// Agent 管理对话上下文、工具调用与长期记忆
type Agent struct {
	provider     provider.Provider
	registry     *tool.Registry
	maxTurns     int
	mem          memory.Memory // 可选记忆模块，nil 表示不使用记忆
	history      []provider.Message
	systemPrompt string
}

// NewAgent 创建无记忆的 Agent（兼容旧版）
func NewAgent(prov provider.Provider, reg *tool.Registry, maxTurns int) *Agent {
	return NewAgentWithMemory(prov, reg, maxTurns, nil)
}

// NewAgentWithMemory 创建带记忆的 Agent，mem 可以为 nil
func NewAgentWithMemory(prov provider.Provider, reg *tool.Registry, maxTurns int, mem memory.Memory) *Agent {
	if maxTurns <= 0 {
		maxTurns = 5
	}
	return &Agent{
		provider: prov,
		registry: reg,
		maxTurns: maxTurns,
		mem:      mem,
		history:  []provider.Message{},
	}
}

// SetSystemPrompt 设置系统提示词（在每次对话前加入消息列表）
func (a *Agent) SetSystemPrompt(prompt string) {
	a.systemPrompt = prompt
}

// ClearHistory 清空对话历史，但保留系统提示词和记忆模块
func (a *Agent) ClearHistory() {
	a.history = []provider.Message{}
}

// Run 执行一轮对话，返回助手最终回复。
// 多次调用自动延续上下文，支持语义记忆检索与存储。
func (a *Agent) Run(userInput string) (string, error) {
	// 1. 检索相关记忆
	memoryContext := ""
	if a.mem != nil {
		query := buildMemoryQuery(userInput, a.history)
		items, err := a.mem.Retrieve(query, 3) // 检索最相关的 3 条记忆
		if err != nil {
			log.Printf("⚠️ 记忆检索失败: %v", err)
		} else if len(items) > 0 {
			memoryContext = "相关记忆：\n" + strings.Join(items, "\n")
		}
	}

	// 2. 构建完整消息列表（系统提示、记忆、历史、当前输入）
	messages := a.buildMessages(userInput, memoryContext)

	// 3. 工具调用循环
	tools := a.registry.ToProviderTools()
	var finalAnswer string
	var finalErr error

	for turn := 0; turn < a.maxTurns; turn++ {
		log.Printf("🔄 Agent 第 %d 轮推理...\n", turn+1)
		respMsg, err := a.provider.Chat(messages, tools)
		if err != nil {
			return "", fmt.Errorf("请求失败: %w", err)
		}

		// 无工具调用：返回文本内容，结束循环
		if len(respMsg.ToolCalls) == 0 {
			finalAnswer = respMsg.Content
			messages = append(messages, respMsg) // 将助手回复也加入当前轮的消息（用于历史构建）
			finalErr = nil
			break
		}

		// 处理工具调用
		log.Printf("🔧 模型请求调用 %d 个工具\n", len(respMsg.ToolCalls))
		messages = append(messages, respMsg)

		for _, tc := range respMsg.ToolCalls {
			log.Printf("  → 调用：%s 参数：%s\n", tc.Function.Name, tc.Function.Arguments)

			args, err := tool.ParseArguments(tc.Function.Arguments)
			if err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
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

	if finalAnswer == "" && finalErr == nil {
		finalErr = fmt.Errorf("达到最大迭代次数 %d，对话未完成", a.maxTurns)
	}

	// 4. 更新对话历史（只保留用户输入和最终助手回答，省略中间工具调用细节）
	a.history = append(a.history,
		provider.Message{Role: "user", Content: userInput},
		provider.Message{Role: "assistant", Content: finalAnswer},
	)

	// 5. 异步存储新记忆（如果有记忆模块）
	if a.mem != nil && finalAnswer != "" {
		go func() {
			summary := fmt.Sprintf("用户: %s\n助手: %s", userInput, finalAnswer)
			if err := a.mem.Store(summary, map[string]string{"type": "conversation"}); err != nil {
				log.Printf("⚠️ 记忆存储失败: %v", err)
			}
		}()
	}

	return finalAnswer, finalErr
}

// buildMessages 构建发送给 Provider 的完整消息列表
func (a *Agent) buildMessages(userInput, memoryContext string) []provider.Message {
	messages := make([]provider.Message, 0)

	// 系统提示
	if a.systemPrompt != "" {
		messages = append(messages, provider.Message{Role: "system", Content: a.systemPrompt})
	}

	// 记忆上下文（作为额外的系统消息）
	if memoryContext != "" {
		messages = append(messages, provider.Message{Role: "system", Content: memoryContext})
	}

	// 历史消息（只保留 user 和 assistant 角色，排除 tool 消息）
	for _, msg := range a.history {
		if msg.Role == "user" || msg.Role == "assistant" {
			messages = append(messages, msg)
		}
	}

	// 当前用户输入
	messages = append(messages, provider.Message{Role: "user", Content: userInput})

	return messages
}

// buildMemoryQuery 根据当前输入和历史生成检索查询
// 简单实现：直接使用用户输入，因为 Memory 内部会做语义嵌入，无需复杂拼接。
func buildMemoryQuery(userInput string, history []provider.Message) string {
	// 可扩展：拼接最后一条助手回复以增强上下文，但一般直接用输入即可
	return userInput
}
