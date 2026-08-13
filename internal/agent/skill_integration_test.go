package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// fakeProvider 记录收到的消息并返回纯文本回复（不真调 LLM）。
type fakeProvider struct {
	lastMsgs []provider.Message
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Chat(ctx context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	f.lastMsgs = append([]provider.Message(nil), msgs...)
	return provider.Message{Role: "assistant", Content: "ok"}, nil
}
func (f *fakeProvider) ChatStream(ctx context.Context, msgs []provider.Message, _ []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestSkillInjectedIntoMessages 验证：命中技能时，SOP 指令被注入为 system 消息。
func TestSkillInjectedIntoMessages(t *testing.T) {
	skillReg := skill.NewRegistry()
	skillReg.Register(&skill.Skill{Name: "report-sop", Version: "1.0.0",
		Description: "写研究报告", Instructions: "第一步：收集事实"})

	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})

	fp := &fakeProvider{}
	router := provider.NewRouter(fp)
	reg := tool.NewRegistry()

	ag := New(Config{
		Router: router, Tools: reg, Prompts: prompts,
		MaxTurns: 1, PromptName: "assistant", Skills: skillReg,
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	if _, err := ag.Run(context.Background(), "帮我写一份研究报告", RunOptions{}); err != nil {
		t.Fatal(err)
	}

	// 断言：发给模型的某条 system 消息包含技能指令
	injected := false
	for _, m := range fp.lastMsgs {
		if m.Role == "system" && strings.Contains(m.Content, "report-sop") && strings.Contains(m.Content, "第一步：收集事实") {
			injected = true
		}
	}
	if !injected {
		t.Fatalf("技能指令未注入，实际消息: %+v", fp.lastMsgs)
	}
}

// TestSkillNotInjectedWhenNoMatch 验证：无关请求不注入技能（懒加载，省 token）。
func TestSkillNotInjectedWhenNoMatch(t *testing.T) {
	skillReg := skill.NewRegistry()
	skillReg.Register(&skill.Skill{Name: "report-sop", Description: "写研究报告", Instructions: "SOP 正文"})

	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})

	fp := &fakeProvider{}
	ag := New(Config{
		Router: provider.NewRouter(fp), Tools: tool.NewRegistry(),
		Prompts: prompts, MaxTurns: 1, PromptName: "assistant", Skills: skillReg,
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	if _, err := ag.Run(context.Background(), "你好，今天天气不错", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, m := range fp.lastMsgs {
		if strings.Contains(m.Content, "report-sop") {
			t.Fatal("无关请求不应注入技能")
		}
	}
}
