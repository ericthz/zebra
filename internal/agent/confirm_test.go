package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// recordingRiskyProvider 返回 ToolCall 并要求调用 risky，同时记录收到的全部消息。
type recordingRiskyProvider struct {
	lastMsgs []provider.Message
}

func (r *recordingRiskyProvider) Name() string { return "rec-risky" }
func (r *recordingRiskyProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	r.lastMsgs = append([]provider.Message(nil), msgs...)
	// 已有工具结果回填 → 出答案；否则要求调 risky
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			return provider.Message{Content: "已完成"}, nil
		}
	}
	return provider.Message{ToolCalls: []provider.ToolCall{{
		ID: "t1", Type: "function",
		Function: provider.FunctionCall{Name: "risky", Arguments: []byte(`{}`)},
	}}}, nil
}
func (r *recordingRiskyProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// riskyProvider 始终要求调用高危工具 risky（风险 2，仅 admin 可用）。
type riskyProvider struct{}

func (riskyProvider) Name() string { return "risky" }
func (riskyProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	// 已执行过（带工具结果回填）→ 出最终答案；否则要求调 risky
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			return provider.Message{Content: "已完成"}, nil
		}
	}
	return provider.Message{ToolCalls: []provider.ToolCall{{
		ID: "t1", Type: "function",
		Function: provider.FunctionCall{Name: "risky", Arguments: []byte(`{}`)},
	}}}, nil
}
func (riskyProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// reactRiskyProvider ReAct 专用：输出结构化行动 JSON（与 riskyTool 对齐）。
type reactRiskyProvider struct{}

func (reactRiskyProvider) Name() string { return "react-risky" }
func (reactRiskyProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	last := msgs[len(msgs)-1].Content
	if strings.Contains(last, "【观察】") {
		return provider.Message{Content: `{"thought":"已处理","answer":"完成"}`}, nil
	}
	return provider.Message{Content: `{"thought":"执行","action":{"name":"risky","args":{}}}`}, nil
}
func (reactRiskyProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// riskyTool 高危工具（复刻 tool 包语义，独立便于本包断言）。
type riskyTool struct{}

func (riskyTool) Name() string        { return "risky" }
func (riskyTool) Description() string { return "高危操作" }
func (riskyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (riskyTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "done", nil
}
func (riskyTool) RiskLevel() int         { return 2 }
func (riskyTool) AllowedRoles() []string { return []string{"admin"} }

// newConfirmAgent 构造带高危工具的 Agent（带系统提示模板）。
func newConfirmAgent(p provider.Provider) *Agent {
	reg := tool.NewRegistry()
	reg.Register(riskyTool{})
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra 助手。"})
	ag := New(Config{Router: provider.NewRouter(p), Tools: reg, Prompts: prompts, MaxTurns: 2, PromptName: "assistant"})
	hist := make([]provider.Message, 0)
	return ag.Bind("s", "admin", "u", &hist)
}

// TestConfirmBlockedWithoutCallback 验证：高危工具在未提供确认回调时被拒绝。
func TestConfirmBlockedWithoutCallback(t *testing.T) {
	fp := &recordingRiskyProvider{}
	ag := newConfirmAgent(fp)

	if _, err := ag.Run(context.Background(), "帮我执行高危操作", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	// 模型应收到"用户拒绝"反馈（工具未真正执行）
	rejected := false
	for _, m := range fp.lastMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "用户拒绝") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("高危工具在无确认时应被拒绝并反馈给模型，实际消息: %+v", fp.lastMsgs)
	}
}

// TestConfirmDenied 验证：确认回调返回 false（用户拒绝）时工具不执行。
func TestConfirmDenied(t *testing.T) {
	fp := &recordingRiskyProvider{}
	ag := newConfirmAgent(fp)

	opts := RunOptions{Confirm: func(string, map[string]interface{}) bool { return false }}
	if _, err := ag.Run(context.Background(), "帮我执行高危操作", opts); err != nil {
		t.Fatal(err)
	}
	rejected := false
	for _, m := range fp.lastMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "用户拒绝") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("确认回调拒绝时应反馈给模型，实际消息: %+v", fp.lastMsgs)
	}
}

// TestConfirmGrantedForRisky 验证：确认回调放行 + 角色匹配时高危工具真正执行。
func TestConfirmGrantedForRisky(t *testing.T) {
	fp := &recordingRiskyProvider{}
	ag := newConfirmAgent(fp)

	opts := RunOptions{Confirm: func(string, map[string]interface{}) bool { return true }}
	if _, err := ag.Run(context.Background(), "帮我执行高危操作", opts); err != nil {
		t.Fatal(err)
	}
	executed := false
	for _, m := range fp.lastMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "done") {
			executed = true
		}
	}
	if !executed {
		t.Fatalf("确认后高危工具应真正执行并返回结果，实际消息: %+v", fp.lastMsgs)
	}
}

// TestConfirmReActShared 验证：ReAct 路径与 toolLoop 共用 confirmTool 逻辑。
func TestConfirmReActShared(t *testing.T) {
	ag := newConfirmAgent(reactRiskyProvider{})

	// 无确认 → 高危工具被拒绝，ReAct 应反馈"用户拒绝"而非执行
	out, err := ag.ReAct(context.Background(), "问题", RunOptions{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "完成") {
		t.Fatalf("ReAct 应处理拒绝后收敛到答案，实际 %q", out)
	}
}
