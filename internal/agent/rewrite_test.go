package agent

import (
	"context"
	"testing"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
)

// rewriteProvider 对改写提示返回固定 JSON。
type rewriteProvider struct{ reply string }

func (r *rewriteProvider) Name() string { return "rewrite" }
func (r *rewriteProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: r.reply}, nil
}
func (r *rewriteProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestRewriteQuery(t *testing.T) {
	ag := newReflectAgent(&rewriteProvider{reply: `{"rewritten":"北京今天的天气怎么样（改写）"}`})
	got, err := ag.RewriteQuery(context.Background(), "北京天气")
	if err != nil {
		t.Fatal(err)
	}
	if got != "北京今天的天气怎么样（改写）" {
		t.Fatalf("改写异常: %q", got)
	}
}

func TestRewriteQueryFallback(t *testing.T) {
	ag := newReflectAgent(&fakeProvider{}) // 返回非 JSON → 回退原问题
	got, err := ag.RewriteQuery(context.Background(), "原问题")
	if err != nil || got != "原问题" {
		t.Fatalf("改写失败应回退原问题: %q %v", got, err)
	}
}

// TestBuildMessagesRewrites P48 集成：开启后检索与最终消息都用改写后的问题。
func TestBuildMessagesRewrites(t *testing.T) {
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})
	ag := New(Config{
		Router:       provider.NewRouter(&rewriteProvider{reply: `{"rewritten":"改写后的问题"}`}),
		Prompts:      prompts,
		PromptName:   "assistant",
		RewriteQuery: true,
	})
	ag.Bind("s", "admin", "u", nil)

	msgs := ag.buildMessages(context.Background(), "原问题")
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "改写后的问题" {
		t.Fatalf("最终用户消息应为改写后的问题: %+v", last)
	}

	// 关闭改写 → 原样
	ag.cfg.RewriteQuery = false
	msgs = ag.buildMessages(context.Background(), "原问题")
	if msgs[len(msgs)-1].Content != "原问题" {
		t.Fatalf("关闭改写后应原样: %q", msgs[len(msgs)-1].Content)
	}
}
