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

// fencedProvider 把合法 JSON 包在 Markdown 代码围栏里（小模型常见行为）。
type fencedProvider struct{}

func (fencedProvider) Name() string { return "fenced" }
func (fencedProvider) Chat(_ context.Context, _ []Message, _ []Tool) (Message, error) {
	return Message{Role: "assistant", Content: "```json\n{\"ok\":true}\n```"}, nil
}
func (fencedProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}

// TestStructuredChatFencedJSON：代码围栏包裹的 JSON 也能通过结构化校验。
func TestStructuredChatFencedJSON(t *testing.T) {
	router := NewRouter(fencedProvider{})
	sch := map[string]interface{}{"type": "object", "required": []interface{}{"ok"}}
	out, err := StructuredChat(context.Background(), router, []Message{{Role: "user", Content: "x"}}, sch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("围栏提取后应含合法 JSON: %s", out)
	}
}

// countingStructuredProvider 记录 Chat 与 ChatJSON 各自的调用次数，
// ChatJSON 永远返回不合 schema 的 JSON（模拟小模型强约束失败）。
type countingStructuredProvider struct {
	chat     int
	chatJSON int
}

func (p *countingStructuredProvider) Name() string { return "count" }
func (p *countingStructuredProvider) Chat(_ context.Context, _ []Message, _ []Tool) (Message, error) {
	p.chat++
	return Message{Role: "assistant", Content: `{"ok":true}`}, nil
}
func (p *countingStructuredProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}
func (p *countingStructuredProvider) ChatJSON(_ context.Context, _ []Message, _ map[string]interface{}) (Message, error) {
	p.chatJSON++
	return Message{Role: "assistant", Content: `{"wrong":1}`}, nil // 不合 schema
}

// TestStructuredChatNoDoubleCallPrimary：主模型强约束失败后不得再次被
// ChatWithFallback 从链首重试（避免同一主模型被调两次、成本翻倍）。
func TestStructuredChatNoDoubleCallPrimary(t *testing.T) {
	primary := &countingStructuredProvider{}
	backup := fakeStructuredProvider{} // 合法 JSON
	router := NewRouter(primary, backup)
	sch := map[string]interface{}{"type": "object", "required": []interface{}{"ok"}}

	out, err := StructuredChat(context.Background(), router, []Message{{Role: "user", Content: "x"}}, sch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("应由备选产出合法 JSON: %s", out)
	}
	if primary.chatJSON != 1 {
		t.Fatalf("主模型强约束应只调 1 次，实际 %d", primary.chatJSON)
	}
	if primary.chat != 0 {
		t.Fatalf("主模型普通调用应为 0（不得重复调主模型），实际 %d", primary.chat)
	}
}

// multiObjectProvider 输出"示例 + 回答"两个对象（小模型常见现象）。
type multiObjectProvider struct{}

func (multiObjectProvider) Name() string { return "multi" }
func (multiObjectProvider) Chat(_ context.Context, _ []Message, _ []Tool) (Message, error) {
	return Message{Role: "assistant", Content: "```json\n{\"ok\":true}\n```\n```json\n{\"ok\":false}\n```"}, nil
}
func (multiObjectProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}

// TestStructuredChatMultiObject：多对象输出按括号配对取第一个完整对象。
func TestStructuredChatMultiObject(t *testing.T) {
	router := NewRouter(multiObjectProvider{})
	sch := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean"}},
	}
	out, err := StructuredChat(context.Background(), router, []Message{{Role: "user", Content: "x"}}, sch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"ok":true`) || strings.Contains(string(out), `"ok":false`) {
		t.Fatalf("应取第一个完整对象: %s", out)
	}
}

// wrappedProvider 单键包裹输出（{"plan":{...}}，小模型常见）。
type wrappedProvider struct{}

func (wrappedProvider) Name() string { return "wrapped" }
func (wrappedProvider) Chat(_ context.Context, _ []Message, _ []Tool) (Message, error) {
	return Message{Role: "assistant", Content: `{"plan":{"ok":true}}`}, nil
}
func (wrappedProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}

// TestStructuredChatWrappedObject：单键包裹的对象可被解包校验。
func TestStructuredChatWrappedObject(t *testing.T) {
	router := NewRouter(wrappedProvider{})
	sch := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"ok": map[string]interface{}{"type": "boolean"}},
	}
	out, err := StructuredChat(context.Background(), router, []Message{{Role: "user", Content: "x"}}, sch)
	if err != nil || !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("包裹对象应被解包: %v %s", err, out)
	}
}

// arrayOfPlanProvider 输出"[单个完整规划对象]"（小模型常见）。
type arrayOfPlanProvider struct{}

func (arrayOfPlanProvider) Name() string { return "arr-plan" }
func (arrayOfPlanProvider) Chat(_ context.Context, _ []Message, _ []Tool) (Message, error) {
	return Message{Role: "assistant", Content: `[{"summary":"查天气","steps":[{"title":"查北京","task":"查询北京天气"}]}]`}, nil
}
func (arrayOfPlanProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}

// TestStructuredChatArrayOfPlan：顶层数组只含一个目标对象 → 解包。
func TestStructuredChatArrayOfPlan(t *testing.T) {
	router := NewRouter(arrayOfPlanProvider{})
	sch := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"summary": map[string]interface{}{"type": "string"}},
		"required":   []interface{}{"steps"},
	}
	out, err := StructuredChat(context.Background(), router, []Message{{Role: "user", Content: "x"}}, sch)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("应解包出对象: %s", out)
	}
	if _, ok := m["steps"]; !ok {
		t.Fatalf("解包对象应含 steps: %s", out)
	}
}

// arrayOfStepsProvider 输出"步骤数组"（把规划直接输出成 [{"title":..,"task":..}]）。
type arrayOfStepsProvider struct{}

func (arrayOfStepsProvider) Name() string { return "arr-steps" }
func (arrayOfStepsProvider) Chat(_ context.Context, _ []Message, _ []Tool) (Message, error) {
	return Message{Role: "assistant", Content: `[{"title":"查北京","task":"查询北京天气"},{"title":"给建议","task":"给出出行建议"}]`}, nil
}
func (arrayOfStepsProvider) ChatStream(context.Context, []Message, []Tool) (<-chan StreamEvent, error) {
	return nil, context.Canceled
}

// TestStructuredChatArrayOfSteps：步骤数组按 schema 包装成 {"steps":[...]}。
func TestStructuredChatArrayOfSteps(t *testing.T) {
	router := NewRouter(arrayOfStepsProvider{})
	sch := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"steps": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "object", "required": []interface{}{"task"}},
			},
		},
		"required": []interface{}{"steps"},
	}
	out, err := StructuredChat(context.Background(), router, []Message{{Role: "user", Content: "x"}}, sch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"steps"`) || !strings.Contains(string(out), "查北京") {
		t.Fatalf("应包装成 steps 对象: %s", out)
	}
}
