package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// plannerProvider 第 1 次调用返回规划 JSON，之后每次返回纯文本（模拟各步骤回答）。
type plannerProvider struct {
	calls int
}

func (p *plannerProvider) Name() string { return "fake-planner" }
func (p *plannerProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	p.calls++
	switch p.calls {
	case 1: // 规划
		return provider.Message{Role: "assistant", Content: `{"summary":"两步计划","steps":[{"title":"步骤甲","task":"任务A"},{"title":"步骤乙","task":"任务B"}]}`}, nil
	case 2: // 步骤甲执行
		return provider.Message{Role: "assistant", Content: "甲的结果"}, nil
	default: // 步骤乙执行
		return provider.Message{Role: "assistant", Content: "乙的结果"}, nil
	}
}
func (p *plannerProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestParsePlan(t *testing.T) {
	// 纯 JSON
	p, err := parsePlan(`{"summary":"s","steps":[{"title":"a","task":"t1"}]}`)
	if err != nil || len(p.Steps) != 1 {
		t.Fatalf("解析失败: %v %+v", err, p)
	}
	// Markdown 包裹
	p2, err := parsePlan("```json\n{\"summary\":\"x\",\"steps\":[{\"title\":\"b\",\"task\":\"t2\"}]}\n```")
	if err != nil || len(p2.Steps) != 1 {
		t.Fatalf("Markdown 包裹解析失败: %v", err)
	}
	// 非法
	if _, err := parsePlan("完全不是 JSON"); err == nil {
		t.Fatal("非法输入应报错")
	}
}

func TestPlanAndExecute(t *testing.T) {
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是助手 {role}"})

	fp := &plannerProvider{}
	ag := New(Config{
		Router: provider.NewRouter(fp), Tools: tool.NewRegistry(), Prompts: prompts,
		MaxTurns: 3, PromptName: "assistant",
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	out, err := ag.PlanAndExecute(context.Background(), "做一个复杂任务", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// 汇总应包含两步标题与各自结果
	for _, want := range []string{"共 2 步", "步骤甲", "步骤乙", "甲的结果", "乙的结果"} {
		if !strings.Contains(out, want) {
			t.Fatalf("汇总缺 %q，实际:\n%s", want, out)
		}
	}
	// 子步骤不应污染会话历史（历史仍为空）
	if len(hist) != 0 {
		t.Fatalf("子步骤不应写入会话历史，实际 %d 条", len(hist))
	}
}

var _ = json.Marshal // 占位
