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
// OpenAI 兼容接口的工具调用按 index 分片下发：首片带 id/type/name，后续片只带
// arguments 碎片（如 `{"arguments":"{\"city\":"}`）。若把每个分片当成完整调用直接
// 上抛，会产生参数残缺、重复条目的 ToolCall（DeepSeek/Kimi 等网关都这样分片）。
// 因此这里按 index 累加 id/name/arguments，流结束时一次性按 index 顺序发出完整调用。
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

		// 工具调用分片累加器：按 index 归并，order 保持首个出现的顺序。
		type acc struct {
			id, typ, name string
			args          []byte
		}
		accs := map[int]*acc{}
		order := []int{}
		flush := func() {
			for _, idx := range order {
				a := accs[idx]
				tc := ToolCall{
					ID:   a.id,
					Type: a.typ,
					Function: FunctionCall{
						Name:      a.name,
						Arguments: a.args,
					},
				}
				ch <- StreamEvent{Type: StreamEventTool, ToolCall: &tc}
			}
		}

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				flush() // 收尾：把累加完整的工具调用一次性发出
				ch <- StreamEvent{Type: StreamEventDone}
				return
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
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
				for _, frag := range d.ToolCalls {
					a, ok := accs[frag.Index]
					if !ok {
						a = &acc{}
						accs[frag.Index] = a
						order = append(order, frag.Index)
					}
					// 分片合并：只覆盖非空字段，arguments 按序拼接
					if frag.ID != "" {
						a.id = frag.ID
					}
					if frag.Type != "" {
						a.typ = frag.Type
					}
					if frag.Function.Name != "" {
						a.name = frag.Function.Name
					}
					if frag.Function.Arguments != "" {
						a.args = append(a.args, frag.Function.Arguments...)
					}
				}
				continue
			}
			if d.Content != "" {
				ch <- StreamEvent{Type: StreamEventDelta, Content: d.Content}
			}
		}
		if err := sc.Err(); err != nil {
			flush() // 出错前把已收集的调用发出，避免丢失
			ch <- StreamEvent{Type: StreamEventError, Err: err}
			return
		}
		// 正常 EOF（无 [DONE]）：部分网关（DeepSeek/Kimi 等）在工具调用后
		// 直接断流不发 [DONE]。若这里直接 return，累加器里的工具调用会
		// 静默丢失、且不会收到 Done 事件 → 上游 agent 误以为没有工具调用。
		// 故 EOF 与 [DONE] 等价收尾：flush + Done（P0-4）。
		flush()
		ch <- StreamEvent{Type: StreamEventDone}
	}()
	return ch, nil
}

// ChatJSON 结构化输出强约束（P17）：
// 通过 OpenAI 的 response_format=json_schema 让模型【生成前】就按 schema 输出，
// 显著降低"生成后解析失败"的概率。这是"强约束"在 provider 层的落地。
// 仅 OpenAI 兼容接口支持；其他 provider 走 StructuredChat 的回退路径。
func (p *OpenAIProvider) ChatJSON(ctx context.Context, messages []Message, jsonSchema map[string]interface{}) (Message, error) {
	payload, err := p.buildPayload(messages, nil, false)
	if err != nil {
		return Message{}, err
	}
	// 在 payload 上附加 response_format
	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return Message{}, err
	}
	body["response_format"] = map[string]interface{}{
		"type":        "json_schema",
		"json_schema": jsonSchema,
	}
	payload, _ = json.Marshal(body)

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
						"type":      "image_url",
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
