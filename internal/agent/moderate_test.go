package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// blockOutModerator 放行输入、拦截包含 marker 的输出。
type blockOutModerator struct{ marker string }

func (m blockOutModerator) Check(text string) (bool, string) {
	if strings.Contains(text, m.marker) {
		return false, "输出包含敏感词: " + m.marker
	}
	return true, ""
}

// TestBlockedOutputNotPersisted：输出未过审核时，不得写入会话历史
// 与分层记忆（违规内容"删除即遗忘"前必须被拦截，不能成为下一轮上下文）。
func TestBlockedOutputNotPersisted(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.CalculatorTool{})
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra 助手。"})

	// 模型固定输出违规内容
	blocked := provider.NewRouter(&staticReplyProvider{reply: "教你赌博下注必胜"})

	hist := make([]provider.Message, 0)
	wm := memory.NewWorkingMemory(10)
	ag := New(Config{
		Router:     blocked,
		Tools:      reg,
		Prompts:    prompts,
		Moderator:  blockOutModerator{marker: "赌博"},
		Mem:        memory.NewManager(wm, nil),
		MaxTurns:   5,
		PromptName: "assistant",
	})
	ag.Bind("s", "admin", "u", &hist)

	_, err := ag.Run(context.Background(), "给我点建议", RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "未通过内容审核") {
		t.Fatalf("应因输出审核失败返回错误，实际: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("违规输出不应写入会话历史，实际 %d 条: %+v", len(hist), hist)
	}
	if items := wm.Recent("s", 10); len(items) != 0 {
		t.Fatalf("违规输出不应写入工作记忆，实际: %+v", items)
	}
}

// 兼容现有静态 provider：固定回复（与 shadow_test 的 staticProvider 同构）。
type staticReplyProvider struct{ reply string }

func (p *staticReplyProvider) Name() string { return "static" }
func (p *staticReplyProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: p.reply}, nil
}
func (p *staticReplyProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}
