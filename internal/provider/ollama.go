// Ollama 原生协议适配（/api/chat），支持 NDJSON 流式。
package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OllamaProvider Ollama 原生接口。
type OllamaProvider struct {
	BaseURL string
	Model   string
	Client  *HTTPClient
}

// Name 实现 Provider。
func (p *OllamaProvider) Name() string { return "ollama" }

// Chat 非流式对话（完整回复）。
func (p *OllamaProvider) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	payload, err := p.buildPayload(messages, tools, false)
	if err != nil {
		return Message{}, err
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/api/chat"
	resp, err := p.Client.Do(ctx, http.MethodPost, url, payload, nil)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Message Message `json:"message"`
		Error   string  `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}
	if result.Error != "" {
		return Message{}, fmt.Errorf("ollama: %s", result.Error)
	}
	return result.Message, nil
}

// ChatStream 流式对话：逐行解析 NDJSON，把文本增量与 tool_calls 转为事件。
func (p *OllamaProvider) ChatStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamEvent, error) {
	payload, err := p.buildPayload(messages, tools, true)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/api/chat"
	resp, err := p.Client.Do(ctx, http.MethodPost, url, payload, nil)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var line struct {
				Message *Message `json:"message"`
				Done    bool     `json:"done"`
				Error   string   `json:"error"`
			}
			if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
				continue
			}
			if line.Error != "" {
				ch <- StreamEvent{Type: StreamEventError, Err: fmt.Errorf("ollama: %s", line.Error)}
				return
			}
			if line.Message == nil {
				continue
			}
			if len(line.Message.ToolCalls) > 0 {
				for i := range line.Message.ToolCalls {
					tc := line.Message.ToolCalls[i]
					ch <- StreamEvent{Type: StreamEventTool, ToolCall: &tc}
				}
				continue
			}
			if line.Message.Content != "" {
				ch <- StreamEvent{Type: StreamEventDelta, Content: line.Message.Content}
			}
			if line.Done {
				ch <- StreamEvent{Type: StreamEventDone}
				return
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Type: StreamEventError, Err: err}
		}
	}()
	return ch, nil
}

func (p *OllamaProvider) buildPayload(messages []Message, tools []Tool, stream bool) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"model":    p.Model,
		"messages": messages,
		"stream":   stream,
		"tools":    tools,
	})
}
