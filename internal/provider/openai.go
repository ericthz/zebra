// OpenAI 兼容协议适配（/v1/chat/completions），支持 SSE 流式与多模态（C14）。
// 因 OpenAI 生态接口高度统一，可同时对接 OpenAI、Ollama 的 OpenAI 兼容端口、
// 及各类国内网关（DeepSeek、Kimi 等）。
package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OpenAIProvider OpenAI 兼容接口。
type OpenAIProvider struct {
	BaseURL string
	Model   string
	APIKey  string
	Client  *HTTPClient
}

// Name 实现 Provider。
func (p *OpenAIProvider) Name() string { return "openai" }

// Chat 非流式对话。
func (p *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	payload, err := p.buildPayload(messages, tools, false)
	if err != nil {
		return Message{}, err
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/v1/chat/completions"
	resp, err := p.Client.Do(ctx, http.MethodPost, url, payload, p.setAuth)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}
	if result.Error != nil {
		return Message{}, fmt.Errorf("openai: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return Message{}, fmt.Errorf("openai: 空 choices")
	}
	return result.Choices[0].Message, nil
}

// ChatStream SSE 流式：解析 `data: {...}` 行，直到 `data: [DONE]`。
func (p *OpenAIProvider) ChatStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamEvent, error) {
	payload, err := p.buildPayload(messages, tools, true)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/v1/chat/completions"
	resp, err := p.Client.Do(ctx, http.MethodPost, url, payload, p.setAuth)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				ch <- StreamEvent{Type: StreamEventDone}
				return
			}
			var chunk struct {
				Choices []struct {
					Delta Message `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			d := chunk.Choices[0].Delta
			if len(d.ToolCalls) > 0 {
				for i := range d.ToolCalls {
					tc := d.ToolCalls[i]
					ch <- StreamEvent{Type: StreamEventTool, ToolCall: &tc}
				}
				continue
			}
			if d.Content != "" {
				ch <- StreamEvent{Type: StreamEventDelta, Content: d.Content}
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Type: StreamEventError, Err: err}
		}
	}()
	return ch, nil
}

func (p *OpenAIProvider) setAuth(req *http.Request) {
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
}

func (p *OpenAIProvider) buildPayload(messages []Message, tools []Tool, stream bool) ([]byte, error) {
	// 多模态：有 ContentParts 的消息转成 OpenAI 的 content 数组（C14）。
	msgs := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		item := map[string]interface{}{"role": m.Role}
		if len(m.ContentParts) > 0 {
			content := make([]map[string]interface{}, 0, len(m.ContentParts))
			for _, part := range m.ContentParts {
				switch part.Type {
				case "image_url":
					content = append(content, map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]string{"url": part.ImageURL},
					})
				default:
					content = append(content, map[string]interface{}{"type": "text", "text": part.Text})
				}
			}
			item["content"] = content
		} else if m.Content != "" {
			item["content"] = m.Content
		} else if len(m.ToolCalls) == 0 && m.ToolCallID != "" {
			item["content"] = ""
		}
		if len(m.ToolCalls) > 0 {
			item["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			item["tool_call_id"] = m.ToolCallID
		}
		if m.Name != "" {
			item["name"] = m.Name
		}
		msgs = append(msgs, item)
	}

	body := map[string]interface{}{
		"model":    p.Model,
		"messages": msgs,
		"stream":   stream,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	return json.Marshal(body)
}
