package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// summarizeProvider 对摘要提示返回固定 JSON。
type summarizeProvider struct{ reply string }

func (s *summarizeProvider) Name() string { return "summary" }
func (s *summarizeProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: s.reply}, nil
}
func (s *summarizeProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestLLMSummarizer(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: "我叫小明"},
		{Role: "assistant", Content: "好的，已记住。"},
	}
	s := LLMSummarizer{Router: provider.NewRouter(&summarizeProvider{reply: `{"summary":"用户叫小明"}`}), MaxChars: 200}
	got, err := s.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if got != "用户叫小明" {
		t.Fatalf("摘要异常: %q", got)
	}
}

func TestLLMSummarizerTruncateAndError(t *testing.T) {
	msgs := []provider.Message{{Role: "user", Content: "你好"}}
	// 超长摘要按 MaxChars 截断
	s := LLMSummarizer{Router: provider.NewRouter(&summarizeProvider{
		reply: `{"summary":"这是一段很长的摘要，超过了长度限制"}`,
	}), MaxChars: 6}
	got, err := s.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(got)) > 7 { // 6 + "…"
		t.Fatalf("摘要未按 MaxChars 截断: %q", got)
	}
	// 模型输出不合规 → 返回错误（由 ContextWindow 保持原消息）
	if _, err := (LLMSummarizer{Router: provider.NewRouter(&fakeProvider{})}).Summarize(context.Background(), msgs); err == nil {
		t.Fatal("摘要失败应返回错误")
	}
	if _, err := (LLMSummarizer{}).Summarize(context.Background(), nil); err == nil {
		t.Fatal("空消息应报错")
	}
}

// TestContextWindowLLMSummarize 集成：超预算时用 LLM 摘要压缩最旧一半。
func TestContextWindowLLMSummarize(t *testing.T) {
	w := &ContextWindow{MaxTokens: 40, Summarizer: LLMSummarizer{
		Router: provider.NewRouter(&summarizeProvider{reply: `{"summary":"早期对话摘要"}`}),
	}}
	msgs := make([]provider.Message, 0, 20)
	for i := 0; i < 20; i++ {
		msgs = append(msgs, provider.Message{Role: "user", Content: "内容内容内容内容"})
	}
	trimmed := w.Trim(msgs)
	hasSummary := false
	for _, m := range trimmed {
		if strings.Contains(m.Content, "早期对话摘要") {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Fatal("超预算时应注入 LLM 摘要")
	}
	if total(trimmed) > w.MaxTokens {
		t.Fatalf("压缩后仍超预算: %d", total(trimmed))
	}
}
