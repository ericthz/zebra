package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// newLoopAgent 构造带 Prompts 的 Agent（buildMessages 需要模板）。
func newLoopAgent(p provider.Provider, maxTurns int) *Agent {
	reg := tool.NewRegistry()
	reg.Register(&tool.CalculatorTool{})
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra 助手。"})
	ag := New(Config{Router: provider.NewRouter(p), Tools: reg, Prompts: prompts, MaxTurns: maxTurns, PromptName: "assistant"})
	hist := make([]provider.Message, 0)
	return ag.Bind("s", "admin", "u", &hist)
}

// alternatingProvider 交替返回 A/B 两种工具调用（模拟 ABABAB 死循环）。
type alternatingProvider struct {
	calls int
}

func (p *alternatingProvider) Name() string { return "alt" }
func (p *alternatingProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	p.calls++
	expr := "1"
	if p.calls%2 == 0 {
		expr = "2"
	}
	return provider.Message{ToolCalls: []provider.ToolCall{{
		ID: "t", Type: "function",
		Function: provider.FunctionCall{Name: "calculator", Arguments: []byte(`{"expression":"` + expr + `"}`)},
	}}}, nil
}
func (p *alternatingProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestLoopDetectionAlternating 验证：A/B 交替循环（ABAB）也能被检出，不死循环到底。
func TestLoopDetectionAlternating(t *testing.T) {
	ag := newLoopAgent(&alternatingProvider{}, 10)
	_, err := ag.Run(context.Background(), "问题", RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "死循环") {
		t.Fatalf("ABAB 交替循环应被检出，实际: %v", err)
	}
}

// dropMidStreamProvider 流式推送 2 段 delta 后中途报错（模拟网络断开）。
type dropMidStreamProvider struct{}

func (dropMidStreamProvider) Name() string { return "drop" }
func (dropMidStreamProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: "【补全】完整答案"}, nil
}
func (dropMidStreamProvider) ChatStream(_ context.Context, _ []provider.Message, _ []provider.Tool) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 3)
	ch <- provider.StreamEvent{Type: provider.StreamEventDelta, Content: "你好，"}
	ch <- provider.StreamEvent{Type: provider.StreamEventDelta, Content: "世界"}
	ch <- provider.StreamEvent{Type: provider.StreamEventError, Err: context.Canceled}
	close(ch)
	return ch, nil
}

// TestStreamDegradeKeepsPrefix 验证：流式中途失败降级非流式时，已发出的 delta
// 不丢失，且补全后的完整答案也要推送给客户端（F-2 修复：此前只推半截前缀）。
func TestStreamDegradeKeepsPrefix(t *testing.T) {
	ag := newLoopAgent(dropMidStreamProvider{}, 3)
	var events []Event
	out, err := ag.RunStream(context.Background(), "问题", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range out {
		if ev.Type == EventDelta {
			events = append(events, ev)
		}
	}
	// 前缀 delta 完整，且补全增量也推送（客户端收到完整答案，与落库一致）
	var streamed string
	for _, ev := range events {
		streamed += ev.Content
	}
	if !strings.Contains(streamed, "你好，世界") {
		t.Fatalf("流式前缀 delta 应完整推送，实际 %q", streamed)
	}
	if !strings.Contains(streamed, "【补全】完整答案") {
		t.Fatalf("降级补全增量应推送给客户端，实际 %q", streamed)
	}
}

// streamUnavailableProvider ChatStream 建立即失败（模拟不支持流式的后端），
// Chat 可正常回答。
type streamUnavailableProvider struct{}

func (streamUnavailableProvider) Name() string { return "no-stream" }
func (streamUnavailableProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: "非流式回答"}, nil
}
func (streamUnavailableProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestStreamEstablishFailureDegradesToDelta 流式建立即失败（无任何 delta）
// 时，降级非流式的结果也必须以 delta 推送（否则客户端只见 done、看不到回答）。
func TestStreamEstablishFailureDegradesToDelta(t *testing.T) {
	ag := newLoopAgent(streamUnavailableProvider{}, 3)
	out, err := ag.RunStream(context.Background(), "问题", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	for ev := range out {
		if ev.Type == EventDelta {
			deltas = append(deltas, ev.Content)
		}
	}
	if len(deltas) == 0 || strings.Join(deltas, "") != "非流式回答" {
		t.Fatalf("降级回答应作为 delta 推送，实际 %v", deltas)
	}
}
