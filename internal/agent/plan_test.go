package agent

import (
	"context"
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
	// 子步骤不污染历史，但"问题→最终汇总"应写入历史（保证多轮上下文连续）
	if len(hist) != 2 {
		t.Fatalf("历史应恰有 1 轮（问题+汇总）共 2 条，实际 %d 条", len(hist))
	}
	if hist[0].Role != "user" || !strings.Contains(hist[0].Content, "做一个复杂任务") {
		t.Fatalf("历史第 1 条应为用户问题: %+v", hist[0])
	}
	if hist[1].Role != "assistant" || !strings.Contains(hist[1].Content, "按规划完成") {
		t.Fatalf("历史第 2 条应为最终汇总: %+v", hist[1])
	}
}

// TestPlanAndExecuteStream：流式规划-执行按阶段推送 phase/delta 事件。
func TestPlanAndExecuteStream(t *testing.T) {
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是助手 {role}"})
	ag := New(Config{
		Router: provider.NewRouter(&plannerProvider{}), Tools: tool.NewRegistry(),
		Prompts: prompts, MaxTurns: 3, PromptName: "assistant",
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	var evs []Event
	out, err := ag.PlanAndExecuteStream(context.Background(), "复杂任务", RunOptions{}, func(ev Event) { evs = append(evs, ev) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "按规划完成") {
		t.Fatalf("流式汇总异常: %s", out)
	}
	phases, hasStep, deltaFound := 0, false, false
	for _, ev := range evs {
		switch ev.Type {
		case EventPhase:
			phases++
			if strings.Contains(ev.Phase, "步骤甲") {
				hasStep = true
			}
		case EventDelta:
			if strings.Contains(ev.Content, "按规划完成") {
				deltaFound = true
			}
		}
	}
	if phases < 4 || !hasStep || !deltaFound {
		t.Fatalf("阶段事件不完整: phases=%d hasStep=%v delta=%v\n%+v", phases, hasStep, deltaFound, evs)
	}
}

// sloppyPlannerProvider 输出被 {"plan":{...}} 包裹、用 step_title 的规划（小模型风格）。
type sloppyPlannerProvider struct{ calls int }

func (p *sloppyPlannerProvider) Name() string { return "sloppy-planner" }
func (p *sloppyPlannerProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	p.calls++
	if p.calls == 1 {
		return provider.Message{Content: "```json\n{\"plan\":{\"title\":\"天气计划\",\"steps\":[{\"step_title\":\"查天气\",\"task\":\"查天气\"}]}}\n```"}, nil
	}
	return provider.Message{Content: "天气结果"}, nil
}
func (p *sloppyPlannerProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestPlanAndExecuteSloppyModel：包裹+step_title 的不规范规划也能执行。
func TestPlanAndExecuteSloppyModel(t *testing.T) {
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是助手 {role}"})
	ag := New(Config{
		Router: provider.NewRouter(&sloppyPlannerProvider{}), Tools: tool.NewRegistry(),
		Prompts: prompts, MaxTurns: 3, PromptName: "assistant",
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	out, err := ag.PlanAndExecute(context.Background(), "查天气", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"共 1 步", "查天气", "天气结果"} {
		if !strings.Contains(out, want) {
			t.Fatalf("归一化规划汇总缺 %q，实际:\n%s", want, out)
		}
	}
}
