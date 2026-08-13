package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// debateProvider 按提示词内容返回首轮观点 / 最终立场 / 评审结论。
type debateProvider struct{}

func (debateProvider) Name() string { return "debate" }
func (debateProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	last := msgs[len(msgs)-1].Content
	switch {
	case strings.Contains(last, "评审"):
		return provider.Message{Content: `{"answer":"评审胜出","winner":"left","reason":"左方更严谨"}`}, nil
	case strings.Contains(last, "对方观点"):
		return provider.Message{Content: "最终立场：综合双方后的结论"}, nil
	default:
		return provider.Message{Content: "首轮观点"}, nil
	}
}
func (debateProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestDebate(t *testing.T) {
	ag := newReflectAgent(debateProvider{})
	answer, reason, err := ag.Debate(context.Background(), "北京好还是上海好", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "评审胜出" || !strings.Contains(reason, "左方更严谨") {
		t.Fatalf("辩论应输出评审结论: %q %q", answer, reason)
	}
}

func TestDebateFallback(t *testing.T) {
	// 评审模型返回非 JSON → 回退左方最终立场
	ag := newReflectAgent(&fakeProvider{})
	answer, reason, err := ag.Debate(context.Background(), "问题", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "ok" || !strings.Contains(reason, "回退") {
		t.Fatalf("评审失败应回退左方立场: %q %q", answer, reason)
	}
}
