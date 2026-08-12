package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type OllamaAnthropicProvider struct {
	BaseURL string
	Model   string
	APIKey  string
}

type anthropicContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

func (p *OllamaAnthropicProvider) Chat(messages []Message, tools []Tool) (Message, error) {
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
