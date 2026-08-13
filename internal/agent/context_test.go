package agent

import (
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		in   string
		min  int
	}{
		{"", 0},
		{"hello world", 1},
		{"你好世界", 1},
		{"这是一个很长的中文句子，用来测试中文 token 估算是否合理。", 8},
	}
	for _, c := range cases {
		n := EstimateTokens(c.in)
		if n < c.min {
			t.Errorf("EstimateTokens(%q)=%d 应 >= %d", c.in, n, c.min)
		}
	}
}

func TestContextWindowTrim(t *testing.T) {
	// 构造一个会超预算的窗口
	w := &ContextWindow{MaxTokens: 40}
	msgs := []provider.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "第1轮很长" + repeat("字", 30)},
		{Role: "assistant", Content: "第1轮回复" + repeat("字", 30)},
		{Role: "user", Content: "第2轮"},
	}
	trimmed := w.Trim(msgs)
	if total(trimmed) > w.MaxTokens {
		t.Fatalf("裁剪后仍超预算: %d > %d", total(trimmed), w.MaxTokens)
	}
	// system 应保留
	hasSys := false
	for _, m := range trimmed {
		if m.Role == "system" {
			hasSys = true
		}
	}
	if !hasSys {
		t.Fatal("system 消息不应被裁剪")
	}
}

func TestContextWindowSummarize(t *testing.T) {
	w := &ContextWindow{MaxTokens: 40, Summarizer: PrefixSummarizer{MaxChars: 20}}
	msgs := make([]provider.Message, 0, 20)
	for i := 0; i < 20; i++ {
		msgs = append(msgs, provider.Message{Role: "user", Content: "内容内容内容内容"})
	}
	trimmed := w.Trim(msgs)
	if total(trimmed) > w.MaxTokens {
		t.Fatalf("摘要压缩后仍超预算: %d", total(trimmed))
	}
	t.Logf("压缩后 %d 条消息, %d tokens", len(trimmed), total(trimmed))
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
