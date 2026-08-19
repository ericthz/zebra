package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOpenAIStreamToolCallFragments 验证 OpenAI 兼容网关按 index 分片下发工具调用时，
// 能按 index 累加 id/name/arguments 并一次性发出完整调用，而不是把每个碎片当成
// 完整调用上抛（后者会产生参数残缺、重复条目的 ToolCall）。
func TestOpenAIStreamToolCallFragments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"calc","arguments":""}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"expr\":"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"1+1\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"now","arguments":""}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{}"}}]}}]}`,
			`data: [DONE]`,
		}
		_, _ = w.Write([]byte(strings.Join(chunks, "\n\n") + "\n"))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		BaseURL: srv.URL,
		Model:   "test-model",
		Client:  NewHTTPClient(5*time.Second, 0, 0),
	}

	ch, err := p.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []ToolCall
	for ev := range ch {
		if ev.Type == StreamEventTool && ev.ToolCall != nil {
			toolCalls = append(toolCalls, *ev.ToolCall)
		}
		if ev.Type == StreamEventDone {
			break
		}
	}
	if len(toolCalls) != 2 {
		t.Fatalf("期望 2 个完整工具调用，实际 %d 个", len(toolCalls))
	}

	first := toolCalls[0]
	if first.ID != "call_1" || first.Function.Name != "calc" {
		t.Fatalf("第一个调用 id/name 错误: %+v", first)
	}
	var args map[string]interface{}
	if err := json.Unmarshal(first.Function.Arguments, &args); err != nil {
		t.Fatalf("arguments 未拼接为合法 JSON: %v (%q)", err, string(first.Function.Arguments))
	}
	if args["expr"] != "1+1" {
		t.Fatalf("arguments 内容错误: %v", args)
	}
	if toolCalls[1].ID != "call_2" || toolCalls[1].Function.Name != "now" {
		t.Fatalf("第二个调用 id/name 错误: %+v", toolCalls[1])
	}
}

// TestOpenAIStreamEOFWithoutDone P0-4：部分网关在工具调用分片后直接断流、
// 不发 [DONE]。EOF 必须等价于 [DONE] 收尾（flush 完整工具调用 + 发 Done 事件），
// 否则累加器中的工具调用会静默丢失，agent 侧误判为"无工具调用"。
func TestOpenAIStreamEOFWithoutDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"calc","arguments":""}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"expr\":"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"2+2\"}"}}]}}]}`,
			// 没有 data: [DONE]，直接 EOF
		}
		_, _ = w.Write([]byte(strings.Join(chunks, "\n\n") + "\n"))
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		BaseURL: srv.URL,
		Model:   "test-model",
		Client:  NewHTTPClient(5*time.Second, 0, 0),
	}

	ch, err := p.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []ToolCall
	gotDone, gotErr := false, false
	for ev := range ch {
		switch ev.Type {
		case StreamEventTool:
			if ev.ToolCall != nil {
				toolCalls = append(toolCalls, *ev.ToolCall)
			}
		case StreamEventDone:
			gotDone = true
		case StreamEventError:
			gotErr = true
		}
	}
	if gotErr {
		t.Fatal("正常 EOF 不应产生 Error 事件")
	}
	if !gotDone {
		t.Fatal("EOF 断流也应发出 Done 事件（P0-4）")
	}
	if len(toolCalls) != 1 {
		t.Fatalf("EOF 后应 flush 出 1 个完整工具调用，实际 %d 个", len(toolCalls))
	}
	tc := toolCalls[0]
	if tc.ID != "call_x" || tc.Function.Name != "calc" {
		t.Fatalf("工具调用 id/name 错误: %+v", tc)
	}
	var args map[string]interface{}
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		t.Fatalf("arguments 未拼接为合法 JSON: %v (%q)", err, string(tc.Function.Arguments))
	}
	if args["expr"] != "2+2" {
		t.Fatalf("arguments 内容错误: %v", args)
	}
}
