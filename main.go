package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ========================
// 公共数据结构
// ========================

type Message struct {
	Role       string     `json:"role"`                 // user, assistant, tool
	Content    string     `json:"content,omitempty"`    // 文本内容
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"` // 助手工具调用请求
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"` // 工具名称（tool 消息时用）
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Tool struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ========================
// 工具定义（天气查询）
// ========================

func weatherTool() Tool {
	return Tool{
		Type: "function",
		Function: FunctionDef{
			Name:        "get_current_weather",
			Description: "获取指定城市的当前天气",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"location": map[string]interface{}{
						"type":        "string",
						"description": "城市名称，例如：北京",
					},
					"unit": map[string]interface{}{
						"type": "string",
						"enum": []string{"celsius", "fahrenheit"},
					},
				},
				"required": []string{"location"},
			},
		},
	}
}

func getCurrentWeatherMock(location, unit string) string {
	return fmt.Sprintf("地点：%s，当前温度：25度（单位：%s），天气：晴朗", location, unit)
}

func getCurrentWeather(location, unit string) string {
	// wttr.in 默认返回摄氏温度，unit 参数可忽略或仅用于显示
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1", url.PathEscape(location))

	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Sprintf("查询天气失败: %v", err)
	}
	defer resp.Body.Close()

	// wttr.in 返回的 JSON 结构
	var data struct {
		CurrentCondition []struct {
			TempC       string `json:"temp_C"`
			WeatherDesc []struct {
				Value string `json:"value"`
			} `json:"weatherDesc"`
		} `json:"current_condition"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Sprintf("解析天气数据失败: %v", err)
	}
	if len(data.CurrentCondition) == 0 {
		return "未获取到天气信息"
	}

	cond := data.CurrentCondition[0]
	desc := cond.WeatherDesc[0].Value
	temp := cond.TempC

	return fmt.Sprintf("地点：%s，当前温度：%s°C，天气：%s", location, temp, desc)
}

// 参数解析（兼容 arguments 为对象或字符串）
func parseArguments(raw json.RawMessage) (map[string]interface{}, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(raw, &args); err == nil {
		return args, nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err != nil {
		return nil, fmt.Errorf("无法解析 arguments: %v", err)
	}
	if err := json.Unmarshal([]byte(str), &args); err != nil {
		return nil, fmt.Errorf("arguments 字符串内容无效: %v", err)
	}
	return args, nil
}

// ========================
// Provider 接口
// ========================

type Provider interface {
	Chat(messages []Message, tools []Tool) (Message, error)
}

// ========================
// Ollama 原生 /api/chat 实现
// ========================

type OllamaNativeProvider struct {
	BaseURL string
	Model   string
}

func (p *OllamaNativeProvider) Chat(messages []Message, tools []Tool) (Message, error) {
	reqBody := struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
		Stream   bool      `json:"stream"`
		Tools    []Tool    `json:"tools,omitempty"`
	}{
		Model:    p.Model,
		Messages: messages,
		Stream:   false,
		Tools:    tools,
	}

	body, _ := json.Marshal(reqBody)
	httpReq, _ := http.NewRequest("POST", p.BaseURL+"/api/chat", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("Ollama 原生接口错误 %s: %s", resp.Status, b)
	}

	var result struct {
		Message Message `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}
	return result.Message, nil
}

// ========================
// Ollama OpenAI 兼容接口 (/v1/chat/completions)
// ========================

type OllamaOpenAIProvider struct {
	BaseURL string
	Model   string
	APIKey  string // Ollama 通常不需要，但保留兼容
}

func (p *OllamaOpenAIProvider) Chat(messages []Message, tools []Tool) (Message, error) {
	reqBody := struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
		Tools    []Tool    `json:"tools,omitempty"`
	}{
		Model:    p.Model,
		Messages: messages,
		Tools:    tools,
	}

	body, _ := json.Marshal(reqBody)
	httpReq, _ := http.NewRequest("POST", p.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("OpenAI 兼容接口错误 %s: %s", resp.Status, b)
	}

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}
	if len(result.Choices) == 0 {
		return Message{}, fmt.Errorf("OpenAI 兼容接口返回空 choices")
	}
	return result.Choices[0].Message, nil
}

// ========================
// Ollama Anthropic 兼容接口 (/v1/messages)
// ========================

type OllamaAnthropicProvider struct {
	BaseURL string
	Model   string
	APIKey  string // 可能不需要，但保留
}

// Anthropic 格式的 content 块
type anthropicContent struct {
	Type  string          `json:"type"`            // text, tool_use
	Text  string          `json:"text,omitempty"`  // 文本
	ID    string          `json:"id,omitempty"`    // tool_use id
	Name  string          `json:"name,omitempty"`  // tool_use 名称
	Input json.RawMessage `json:"input,omitempty"` // 工具输入
}

func (p *OllamaAnthropicProvider) Chat(messages []Message, tools []Tool) (Message, error) {
	// 转换消息为 Anthropic 格式
	var anthropicMsgs []map[string]interface{}
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			anthropicMsgs = append(anthropicMsgs, map[string]interface{}{
				"role":    "user",
				"content": msg.Content,
			})
		case "assistant":
			content := []interface{}{}
			if msg.Content != "" {
				content = append(content, anthropicContent{Type: "text", Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				content = append(content, anthropicContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: tc.Function.Arguments,
				})
			}
			anthropicMsgs = append(anthropicMsgs, map[string]interface{}{
				"role":    "assistant",
				"content": content,
			})
		case "tool":
			// Anthropic 要求工具结果作为 user 消息
			content := []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": msg.ToolCallID,
					"content":     msg.Content,
				},
			}
			anthropicMsgs = append(anthropicMsgs, map[string]interface{}{
				"role":    "user",
				"content": content,
			})
		}
	}

	// 转换工具定义
	type anthropicToolDef struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		InputSchema map[string]interface{} `json:"input_schema"`
	}
	var anthropicTools []anthropicToolDef
	for _, t := range tools {
		anthropicTools = append(anthropicTools, anthropicToolDef{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	reqBody := map[string]interface{}{
		"model":      p.Model,
		"max_tokens": 1024,
		"messages":   anthropicMsgs,
		"tools":      anthropicTools,
	}

	body, _ := json.Marshal(reqBody)
	httpReq, _ := http.NewRequest("POST", p.BaseURL+"/v1/messages", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if p.APIKey != "" {
		httpReq.Header.Set("x-api-key", p.APIKey)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("Anthropic 兼容接口错误 %s: %s", resp.Status, b)
	}

	var result struct {
		Content    []anthropicContent `json:"content"`
		StopReason string             `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}

	// 转换回公共 Message
	var assistantMsg Message
	assistantMsg.Role = "assistant"
	for _, c := range result.Content {
		switch c.Type {
		case "text":
			assistantMsg.Content += c.Text
		case "tool_use":
			tc := ToolCall{
				ID:   c.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      c.Name,
					Arguments: c.Input,
				},
			}
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, tc)
		}
	}
	return assistantMsg, nil
}

// ========================
// 工具调用循环
// ========================

func runConversation(provider Provider, userInput string, tools []Tool) {
	messages := []Message{
		{Role: "user", Content: userInput},
	}

	for i := 0; i < 5; i++ {
		fmt.Printf("\n🔄 第 %d 轮调用...\n", i+1)
		respMsg, err := provider.Chat(messages, tools)
		if err != nil {
			fmt.Fprintf(os.Stderr, "请求失败: %v\n", err)
			return
		}

		if len(respMsg.ToolCalls) > 0 {
			fmt.Println("🔧 模型请求调用工具：")
			messages = append(messages, respMsg)

			for _, tc := range respMsg.ToolCalls {
				fmt.Printf("  → 函数：%s，参数：%s\n", tc.Function.Name, tc.Function.Arguments)
				args, err := parseArguments(tc.Function.Arguments)
				if err != nil {
					fmt.Fprintf(os.Stderr, "参数解析失败: %v\n", err)
					return
				}
				location, _ := args["location"].(string)
				unit := "celsius"
				if u, ok := args["unit"].(string); ok {
					unit = u
				}
				result := getCurrentWeather(location, unit)
				fmt.Printf("  → 执行结果：%s\n", result)

				messages = append(messages, Message{
					Role:       "tool",
					Content:    result,
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
				})
			}
			continue
		}

		fmt.Println("🤖 最终回答：", respMsg.Content)
		return
	}
	fmt.Println("⚠️ 达到最大迭代次数，对话停止。")
}

// ========================
// .env 文件加载
// ========================

func loadEnv(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过空行和注释
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// 移除引号（支持双引号或单引号）
		if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
			(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			value = value[1 : len(value)-1]
		}
		os.Setenv(key, value)
	}
	return scanner.Err()
}

// ========================
// 主函数
// ========================

func main() {
	// 加载 .env 文件
	if err := loadEnv(".env"); err != nil {
		fmt.Println("未找到 .env 文件，使用默认配置")
	}

	// 读取配置
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "llama3.1"
	}
	providerName := strings.ToLower(os.Getenv("OLLAMA_PROVIDER"))
	if providerName == "" {
		providerName = "ollama"
	}
	apiKey := os.Getenv("OLLAMA_API_KEY") // 可选

	// 根据 provider 创建实例
	var provider Provider
	switch providerName {
	case "ollama", "native":
		provider = &OllamaNativeProvider{BaseURL: baseURL, Model: model}
	case "openai":
		provider = &OllamaOpenAIProvider{BaseURL: baseURL, Model: model, APIKey: apiKey}
	case "anthropic":
		provider = &OllamaAnthropicProvider{BaseURL: baseURL, Model: model, APIKey: apiKey}
	default:
		fmt.Fprintf(os.Stderr, "不支持的接口类型: %s（支持 ollama / openai / anthropic）\n", providerName)
		os.Exit(1)
	}

	fmt.Printf("🚀 使用 Ollama 接口类型: %s，模型: %s，服务地址: %s\n", providerName, model, baseURL)

	// 启动对话
	userInput := "北京今天天气怎么样？"
	runConversation(provider, userInput, []Tool{weatherTool()})
}
