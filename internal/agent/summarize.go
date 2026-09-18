// LLM 摘要压缩：把旧对话压成语义摘要，替代"截断式"PrefixSummarizer。
//
// 背景：上下文超预算时，PrefixSummarizer 只是机械截断，会丢关键信息。
// LLMSummarizer 让模型提炼"关键事实/结论/待办"，摘要以 system 消息回填，
// 长对话下既控 token 又保语义。模型不可用/输出异常时返回错误，
// 由 ContextWindow.Trim 保持原消息（容错，不劣化）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ericthz/zebra/internal/provider"
)

// summarySchema 摘要输出 JSON Schema。
var summarySchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"summary": map[string]interface{}{"type": "string"},
	},
	"required": []string{"summary"},
}

// LLMSummarizer 用 LLM 压缩对话摘要。
type LLMSummarizer struct {
	Router   *provider.Router
	MaxChars int // 摘要长度上限（rune 数，默认 600）
}

// Summarize 把 messages 压缩成一段中文摘要。
func (s LLMSummarizer) Summarize(ctx context.Context, messages []provider.Message) (string, error) {
	var b strings.Builder
	for _, m := range messages {
		if (m.Role == "user" || m.Role == "assistant") && m.Content != "" {
			fmt.Fprintf(&b, "%s：%s\n", m.Role, m.Content)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("无可摘要内容")
	}
	prompt := "请把下面的对话压缩成一段简洁的中文摘要，保留关键事实、结论与待办事项：\n" +
		truncateRunes(b.String(), 3000)
	data, err := provider.StructuredChat(ctx, s.Router,
		[]provider.Message{{Role: "user", Content: prompt}}, summarySchema)
	if err != nil {
		return "", err
	}
	var out struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(data, &out); err != nil || strings.TrimSpace(out.Summary) == "" {
		return "", fmt.Errorf("摘要解析失败")
	}
	max := s.MaxChars
	if max <= 0 {
		max = 600
	}
	return truncateRunes(out.Summary, max), nil
}

// truncateRunes 按 rune 截断（多字节安全）。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
