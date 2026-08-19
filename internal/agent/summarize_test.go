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
	// 预算小到"2 条消息即超预算"：裁剪保留 2 条后，摘要器才被触发
	w := &ContextWindow{MaxTokens: 10, Summarizer: LLMSummarizer{
		Router: provider.NewRouter(&summarizeProvider{reply: `{"summary":"早期对话摘要"}`}),
	}}
	msgs := make([]provider.Message, 0, 20)
	for i := 0; i < 20; i++ {
		msgs = append(msgs, provider.Message{Role: "user", Content: "内容内容内容内容"})
	}
	before := total(msgs)
	trimmed := w.Trim(context.Background(), msgs)
	hasSummary := false
	for _, m := range trimmed {
		if strings.Contains(m.Content, "早期对话摘要") {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Fatal("超预算时应注入 LLM 摘要")
	}
	if total(trimmed) >= before {
		t.Fatalf("摘要压缩后应减少 token: %d -> %d", before, total(trimmed))
	}
}

// cancelSummarizer 摘要时检查 ctx 是否已取消（验证 Trim 透传 ctx，不硬编码 Background）。
type cancelSummarizer struct{ cancelled bool }

func (c *cancelSummarizer) Summarize(ctx context.Context, _ []provider.Message) (string, error) {
	if ctx.Err() != nil {
		c.cancelled = true
		return "", ctx.Err()
	}
	return "摘要", nil
}

// TestTrimPropagatesContext 验证：Trim 把调用方 ctx 透传给 Summarizer（取消传播）。
func TestTrimPropagatesContext(t *testing.T) {
	cs := &cancelSummarizer{}
	w := &ContextWindow{MaxTokens: 1, Summarizer: cs}
	msgs := make([]provider.Message, 0, 10)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, provider.Message{Role: "user", Content: "很长很长的内容内容内容"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消的 ctx

	_ = w.Trim(ctx, msgs)
	if !cs.cancelled {
		t.Fatal("Summarizer 应收到已取消的 ctx（Trim 不应使用 context.Background）")
	}
}

// TestTrimSummarizeRecheckBudget 验证：摘要后复查预算，仍超则继续压缩（不死循环）。
func TestTrimSummarizeRecheckBudget(t *testing.T) {
	// 摘要器只压一点点：第一次摘要后仍超预算，循环应继续压缩直到有进展
	cs := &shrinkSummarizer{shrink: 1} // 每次只减 1 token
	w := &ContextWindow{MaxTokens: 5, Summarizer: cs}
	msgs := make([]provider.Message, 0, 8)
	for i := 0; i < 8; i++ {
		msgs = append(msgs, provider.Message{Role: "user", Content: "内容内容内容内容"})
	}
	before := total(msgs)
	trimmed := w.Trim(context.Background(), msgs)
	if total(trimmed) >= before {
		t.Fatalf("应持续压缩直到有进展: %d -> %d", before, total(trimmed))
	}
}

// shrinkSummarizer 每次摘要只减少固定 token 数，用于验证循环复查。
type shrinkSummarizer struct{ shrink int }

func (s *shrinkSummarizer) Summarize(_ context.Context, msgs []provider.Message) (string, error) {
	// 生成比原文短 shrink 个单位的内容
	n := total(msgs) - s.shrink
	if n < 0 {
		n = 0
	}
	return strings.Repeat("短", n), nil
}
