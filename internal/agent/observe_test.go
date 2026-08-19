package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// TestOnSkillHook P31：命中技能时 OnSkill 上报技能名（学习/可观测钩子）。
func TestOnSkillHook(t *testing.T) {
	skillReg := skill.NewRegistry()
	skillReg.Register(&skill.Skill{Name: "report-sop", Version: "1.0.0",
		Description: "写研究报告", Instructions: "第一步：收集事实"})
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})

	var injected []string
	ag := New(Config{
		Router: provider.NewRouter(&fakeProvider{}), Tools: tool.NewRegistry(),
		Prompts: prompts, MaxTurns: 1, PromptName: "assistant",
		Skills:  skillReg,
		OnSkill: func(names []string) { injected = append(injected, names...) },
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	if _, err := ag.Run(context.Background(), "帮我写一份研究报告", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(injected, []string{"report-sop"}) {
		t.Fatalf("OnSkill 应上报 report-sop，实际 %v", injected)
	}
}

// echoTool 固定成功的工具（OnTool 钩子测试用）。
type echoTool struct{}

func (echoTool) Name() string        { return "tool-a" }
func (echoTool) Description() string { return "echo tool" }
func (echoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (echoTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "echo-done", nil
}

// oneCallProvider 第一轮请求 tool-a，第二轮返回纯文本。
type oneCallProvider struct {
	second bool
}

func (f *oneCallProvider) Name() string { return "fake" }
func (f *oneCallProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	if !f.second && !hasToolResult(msgs) {
		return provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{
			{ID: "c1", Type: "function", Function: provider.FunctionCall{Name: "tool-a", Arguments: json.RawMessage(`{"x":1}`)}},
		}}, nil
	}
	f.second = true
	return provider.Message{Role: "assistant", Content: "done"}, nil
}
func (f *oneCallProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestOnToolHook P31：工具真实执行时 OnTool 上报名称/参数/结果。
func TestOnToolHook(t *testing.T) {
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})
	reg := tool.NewRegistry()
	reg.Register(echoTool{})

	var (
		gotName string
		gotArgs map[string]interface{}
		gotOK   bool
		gotErr  error
	)
	ag := New(Config{
		Router: provider.NewRouter(&oneCallProvider{}), Tools: reg,
		Prompts: prompts, MaxTurns: 3, PromptName: "assistant",
		OnTool: func(name string, args map[string]interface{}, ok bool, err error) {
			gotName, gotArgs, gotOK, gotErr = name, args, ok, err
		},
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	answer, err := ag.Run(context.Background(), "调用一下工具", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "done" {
		t.Fatalf("最终回答异常: %q", answer)
	}
	if gotName != "tool-a" || !gotOK || gotErr != nil {
		t.Fatalf("OnTool 上报异常: name=%s ok=%v err=%v", gotName, gotOK, gotErr)
	}
	if gotArgs["x"] != float64(1) {
		t.Fatalf("OnTool 参数异常: %v", gotArgs)
	}
}

// TestSkillEventEmitted P61：技能注入时发出 skill 流式事件（前端轨迹展示）。
func TestSkillEventEmitted(t *testing.T) {
	skillReg := skill.NewRegistry()
	skillReg.Register(&skill.Skill{Name: "report-sop", Description: "写报告", Instructions: "SOP"})
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})
	ag := New(Config{
		Router: provider.NewRouter(&fakeProvider{}), Prompts: prompts,
		PromptName: "assistant", Skills: skillReg,
	})
	ag.Bind("s", "admin", "u", nil)

	var evs []Event
	ag.buildMessages(context.Background(), "帮我写一份研究报告", nil, func(ev Event) { evs = append(evs, ev) })
	found := false
	for _, ev := range evs {
		if ev.Type == EventSkill && ev.Skill == "report-sop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应发出技能事件: %+v", evs)
	}
}
