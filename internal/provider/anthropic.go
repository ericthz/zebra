// Anthropic 协议适配（/v1/messages）。
// 本实现只演示非流式：ChatStream 回退为"Chat 一次 + 单个 delta + done"，
// 展示"接口必须支持流式、但底层可不流式"的兼容模式。
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicProvider Anthropic 接口。
type AnthropicProvider struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int // 单次回复 token 上限（0 = 默认 2048）
	Client    *HTTPClient
}

// Name 实现 Provider。
func (p *AnthropicProvider) Name() string { return "anthropic" }

// Chat 非流式对话，完成 Anthropic ↔ 统一 Message 的双向转换。
func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	maxTokens := p.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	body, err := json.Marshal(map[string]interface{}{
		"model":      p.Model,
		"max_tokens": maxTokens,
		"messages":   toAnthropicMessages(messages),
		"tools":      toAnthropicTools(tools),
	})
	if err != nil {
		return Message{}, err
	}

	url := strings.TrimRight(p.BaseURL, "/") + "/v1/messages"
	resp, err := p.Client.Do(ctx, http.MethodPost, url, body, func(req *http.Request) {
		req.Header.Set("anthropic-version", "2023-06-01")
		if p.APIKey != "" {
			req.Header.Set("x-api-key", p.APIKey)
		}
	})
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}
	if result.Error != nil {
		return Message{}, fmt.Errorf("anthropic: %s", result.Error.Message)
	}

	var msg Message
	msg.Role = "assistant"
	for _, c := range result.Content {
		switch c.Type {
		case "text":
			msg.Content += c.Text
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID: c.ID, Type: "function",
				Function: FunctionCall{Name: c.Name, Arguments: c.Input},
			})
		}
	}
	return msg, nil
}

// ChatStream 非流式回退：包装 Chat 的结果为流式事件。
func (p *AnthropicProvider) ChatStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 8)
	go func() {
		defer close(ch)
		msg, err := p.Chat(ctx, messages, tools)
		if err != nil {
			ch <- StreamEvent{Type: StreamEventError, Err: err}
			return
		}
		if msg.Content != "" {
			ch <- StreamEvent{Type: StreamEventDelta, Content: msg.Content}
		}
		for i := range msg.ToolCalls {
			tc := msg.ToolCalls[i]
			ch <- StreamEvent{Type: StreamEventTool, ToolCall: &tc}
		}
		ch <- StreamEvent{Type: StreamEventDone}
	}()
	return ch, nil
}

func toAnthropicMessages(messages []Message) []map[string]interface{} {
	var out []map[string]interface{}
	for _, m := range messages {
		switch m.Role {
		case "user":
			if len(m.ContentParts) > 0 {
				content := make([]map[string]interface{}, 0, len(m.ContentParts))
				for _, p := range m.ContentParts {
					if p.Type == "image_url" {
						content = append(content, map[string]interface{}{
							"type": "image", "source": map[string]interface{}{
								"type": "url", "url": p.ImageURL,
							},
						})
					} else {
						content = append(content, map[string]interface{}{"type": "text", "text": p.Text})
					}
				}
				out = append(out, map[string]interface{}{"role": "user", "content": content})
			} else {
				out = append(out, map[string]interface{}{"role": "user", "content": m.Content})
			}
		case "assistant":
			content := []interface{}{}
			if m.Content != "" {
				content = append(content, map[string]interface{}{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				content = append(content, map[string]interface{}{
					"type": "tool_use", "id": tc.ID, "name": tc.Function.Name,
					"input": json.RawMessage(tc.Function.Arguments),
				})
			}
			out = append(out, map[string]interface{}{"role": "assistant", "content": content})
		case "tool":
			out = append(out, map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content},
				},
			})
		}
	}
	return out
}

func toAnthropicTools(tools []Tool) []map[string]interface{} {
	var out []map[string]interface{}
	for _, t := range tools {
		out = append(out, map[string]interface{}{
			"name": t.Function.Name, "description": t.Function.Description,
			"input_schema": t.Function.Parameters,
		})
	}
	return out
}
