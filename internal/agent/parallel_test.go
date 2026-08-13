package agent

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// slowTool 带延迟的工具，用活跃计数检测并发度。
type slowTool struct {
	name   string
	delay  time.Duration
	active *int32
	max    *int32
	mu     *sync.Mutex
	calls  *[]string
}

func (t *slowTool) Name() string        { return t.name }
func (t *slowTool) Description() string { return "slow tool " + t.name }
func (t *slowTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *slowTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	n := atomic.AddInt32(t.active, 1)
	// 更新最大并发
	for {
		m := atomic.LoadInt32(t.max)
		if n <= m || atomic.CompareAndSwapInt32(t.max, m, n) {
			break
		}
	}
	time.Sleep(t.delay)
	atomic.AddInt32(t.active, -1)
	t.mu.Lock()
	*t.calls = append(*t.calls, t.name)
	t.mu.Unlock()
	return t.name + "-done", nil
}

// twoCallsProvider 第一轮返回两个工具调用，第二轮返回纯文本。
type twoCallsProvider struct {
	secondMsgs []provider.Message // 记录第二轮收到的消息（含工具结果，用于校验顺序）
}

func (f *twoCallsProvider) Name() string { return "fake" }
func (f *twoCallsProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	// 第一轮：没有任何工具结果 → 让模型请求两个工具
	if len(f.secondMsgs) == 0 && !hasToolResult(msgs) {
		return provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "tool-a", Arguments: json.RawMessage("{}")}},
			{ID: "c2", Type: "function", Function: provider.FunctionCall{Name: "tool-b", Arguments: json.RawMessage("{}")}},
		}}, nil
	}
	f.secondMsgs = msgs
	return provider.Message{Role: "assistant", Content: "all done"}, nil
}
func (f *twoCallsProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func hasToolResult(msgs []provider.Message) bool {
	for _, m := range msgs {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

// TestParallelToolCalls 验证：同一轮多个工具被并发执行、且结果顺序保持。
func TestParallelToolCalls(t *testing.T) {
	var active, maxActive int32
	var mu sync.Mutex
	var calls []string

	reg := tool.NewRegistry()
	reg.Register(&slowTool{name: "tool-a", delay: 100 * time.Millisecond, active: &active, max: &maxActive, mu: &mu, calls: &calls})
	reg.Register(&slowTool{name: "tool-b", delay: 100 * time.Millisecond, active: &active, max: &maxActive, mu: &mu, calls: &calls})

	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "sys"})

	fp := &twoCallsProvider{}
	ag := New(Config{
		Router: provider.NewRouter(fp), Tools: reg, Prompts: prompts,
		MaxTurns: 2, PromptName: "assistant",
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	start := time.Now()
	answer, err := ag.Run(context.Background(), "do both", RunOptions{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "all done" {
		t.Fatalf("最终回答错误: %q", answer)
	}

	// 并发度应 >1（两个工具并行执行）
	if atomic.LoadInt32(&maxActive) < 2 {
		t.Fatalf("期望并发执行（maxActive>=2），实际 %d", maxActive)
	}
	// 两个工具都被调用
	if len(calls) != 2 {
		t.Fatalf("两个工具都应执行，实际 %d: %v", len(calls), calls)
	}
	// 串行会耗时 ~200ms，并行应显著小于
	if elapsed > 180*time.Millisecond {
		t.Fatalf("并行执行应远快于串行，实际耗时 %v", elapsed)
	}
	t.Logf("并行执行耗时 %v，最大并发 %d，调用顺序 %v", elapsed, maxActive, calls)

	// 结果顺序保持：第二轮消息中 tool 结果应按 c1,c2 顺序
	if len(fp.secondMsgs) < 2 {
		t.Fatalf("第二轮应包含工具结果，实际 %d 条", len(fp.secondMsgs))
	}
	got := []string{}
	for _, m := range fp.secondMsgs {
		if m.Role == "tool" {
			got = append(got, m.Name)
		}
	}
	if len(got) != 2 || got[0] != "tool-a" || got[1] != "tool-b" {
		t.Fatalf("工具结果顺序应保持 [tool-a tool-b]，实际 %v", got)
	}
}
