package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// reactProvider 按上一步是否有观察回填返回"行动"或"答案"。
type reactProvider struct{}

func (reactProvider) Name() string { return "react" }
func (reactProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	last := msgs[len(msgs)-1].Content
	if strings.Contains(last, "【观察】") {
		return provider.Message{Content: `{"thought":"已完成","answer":"3"}`}, nil
	}
	return provider.Message{Content: `{"thought":"先算一下","action":{"name":"calculator","args":{"expression":"1+2"}}}`}, nil
}
func (reactProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// loopProvider 每步都要求行动（用于验证最大步数保护）。
type loopProvider struct{}

func (loopProvider) Name() string { return "loop" }
func (loopProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: `{"thought":"再来","action":{"name":"calculator","args":{"expression":"1"}}}`}, nil
}
func (loopProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func newReActAgent(p provider.Provider) *Agent {
	reg := tool.NewRegistry()
	reg.Register(&tool.CalculatorTool{})
	ag := New(Config{Router: provider.NewRouter(p), Tools: reg, MaxTurns: 3})
	hist := make([]provider.Message, 0)
	return ag.Bind("s", "admin", "u", &hist)
}

// TestReactSystemPromptIncludesTools 验证 ReAct 系统提示注入了工具列表，
// 避免模型凭名字猜工具（缺工具描述时大概率调错）。
func TestReactSystemPromptIncludesTools(t *testing.T) {
	p := reactSystemPrompt([]provider.Tool{{
		Type: "function",
		Function: provider.FunctionDef{
			Name:        "calculator",
			Description: "计算数学表达式",
			Parameters:  map[string]interface{}{"type": "object"},
		},
	}})
	if !strings.Contains(p, "calculator") || !strings.Contains(p, "计算数学表达式") {
		t.Fatalf("系统提示应包含工具名与描述: %s", p)
	}
}

func TestReAct(t *testing.T) {
	ag := newReActAgent(reactProvider{})
	got, err := ag.ReAct(context.Background(), "1+2 等于多少", RunOptions{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != "3" {
		t.Fatalf("ReAct 最终答案应为 3，实际 %q", got)
	}
}

func TestReActMaxSteps(t *testing.T) {
	ag := newReActAgent(loopProvider{})
	_, err := ag.ReAct(context.Background(), "问题", RunOptions{}, 2)
	if err == nil || !strings.Contains(err.Error(), "最大步数") {
		t.Fatalf("死循环应触发最大步数保护: %v", err)
	}
}

// TestReActStream：流式 ReAct 推送 思考/行动/观察/结论 轨迹。
func TestReActStream(t *testing.T) {
	ag := newReActAgent(reactProvider{})
	var evs []Event
	out, err := ag.ReActStream(context.Background(), "1+2 等于多少", RunOptions{}, 3, func(ev Event) { evs = append(evs, ev) })
	if err != nil || out != "3" {
		t.Fatalf("ReActStream 异常: %q %v", out, err)
	}
	var sawThink, sawTool, sawObserve, sawAnswer bool
	for _, ev := range evs {
		switch {
		case ev.Type == EventPhase && strings.Contains(ev.Phase, "思考"):
			sawThink = true
		case ev.Type == EventTool && ev.Name == "calculator":
			sawTool = true
		case ev.Type == EventPhase && strings.Contains(ev.Phase, "观察"):
			sawObserve = true
		case ev.Type == EventDelta && ev.Content == "3":
			sawAnswer = true
		}
	}
	if !sawThink || !sawTool || !sawObserve || !sawAnswer {
		t.Fatalf("ReAct 轨迹事件不完整: think=%v tool=%v observe=%v answer=%v\n%+v", sawThink, sawTool, sawObserve, sawAnswer, evs)
	}
}
