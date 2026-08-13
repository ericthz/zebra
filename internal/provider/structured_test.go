package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// captureRoundTripper 进程内捕获请求体（无网可跑）。
type captureRoundTripper struct{ body []byte }

func (c *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	c.body = b
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
		`{"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}]}`))}, nil
}

// TestChatJSONPayload 验证 response_format 被注入请求体。
func TestChatJSONPayload(t *testing.T) {
	rt := &captureRoundTripper{}
	cli := NewHTTPClient(5*time.Second, 0, 100*time.Millisecond) // 初始化熔断器
	cli.client = &http.Client{Transport: rt}
	p := &OpenAIProvider{BaseURL: "http://x", Model: "m", Client: cli}
	schemaJSON := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean"}}}

	_, err := p.ChatJSON(context.Background(), []Message{{Role: "user", Content: "hi"}}, schemaJSON)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rt.body, &body); err != nil {
		t.Fatal(err)
	}
	rf, ok := body["response_format"].(map[string]interface{})
	if !ok {
		t.Fatalf("缺 response_format: %s", rt.body)
	}
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format.type 错误: %v", rf["type"])
	}
}

// fakeStructuredProvider 普通 Chat 返回合法 JSON（验证回退路径 + 校验通过）。
type fakeStructuredProvider struct{}

func (fakeStructuredProvider) Name() string { return "fake" }
func (fakeStructuredProvider) Chat(_ context.Context, _ []Message, _ []Tool) (Message, error) {
	return Message{Role: "assistant", Content: `{"ok":true}`}, nil
}
func (fakeStructuredProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}

// TestStructuredChatFallback 验证：无强约束 provider 时走普通 Chat + 校验。
func TestStructuredChatFallback(t *testing.T) {
	router := NewRouter(fakeStructuredProvider{})
	sch := map[string]interface{}{"type": "object", "required": []interface{}{"ok"}}
	out, err := StructuredChat(context.Background(), router, []Message{{Role: "user", Content: "x"}}, sch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("回退输出不符: %s", out)
	}
}
