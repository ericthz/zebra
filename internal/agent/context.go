// C11 上下文工程：token 估算、滑动窗口、对话摘要压缩。
//
// 目标：在模型上下文窗口（context window）预算内，塞进"最有价值"的消息。
// 策略：预算内保留全部 → 超预算则从最旧消息开始裁剪（保留 system/记忆与最新几轮）；
// 若裁剪后仍超预算，交给 Summarizer 把最旧一段压成摘要。
package agent

import (
	"context"
	"strings"
	"unicode"

	"github.com/ericthz/zebra/internal/provider"
)

// EstimateTokens 启发式 token 估算：
// 中文按每字约 1 token，英文按每词约 1.3 token，另加固定开销。
// 生产建议用分词器（tiktoken 等）精确计算。
func EstimateTokens(s string) int {
	cjk, words := 0, 0
	inWord := false
	for _, r := range s {
		if unicode.In(r, unicode.Han) {
			cjk++
			inWord = false
		} else if unicode.IsSpace(r) || unicode.IsPunct(r) {
			inWord = false
		} else {
			if !inWord {
				words++
				inWord = true
			}
		}
	}
	return cjk + (words*13)/10 + 4
}

// MessageTokens 估算单条消息占用的 token。
func MessageTokens(m provider.Message) int {
	n := EstimateTokens(m.Content)
	for _, p := range m.ContentParts {
		n += EstimateTokens(p.Text) + 2 // 图片按固定成本估算
	}
	for _, tc := range m.ToolCalls {
		n += EstimateTokens(string(tc.Function.Arguments)) + 8
	}
	return n
}

// Summarizer 对话摘要压缩器（可选，可插拔 LLM 实现）。
type Summarizer interface {
	Summarize(ctx context.Context, messages []provider.Message) (string, error)
}

// PrefixSummarizer 最简回退实现：把最旧一段截断拼接。
// 生产替换为"调用 LLM 生成结构化摘要"，接口不变。
type PrefixSummarizer struct {
	MaxChars int
}

// Summarize 把消息内容压缩成一段截断文本。
func (p PrefixSummarizer) Summarize(_ context.Context, messages []provider.Message) (string, error) {
	var parts []string
	for _, m := range messages {
		if m.Content != "" {
			parts = append(parts, m.Role+": "+m.Content)
		}
	}
	text := strings.Join(parts, "\n")
	if len([]rune(text)) > p.MaxChars {
		text = string([]rune(text)[:p.MaxChars]) + "…（已截断）"
	}
	return text, nil
}

// ContextWindow 上下文窗口预算管理。
type ContextWindow struct {
	MaxTokens  int // 发送给模型的 token 预算（应 < 模型 context window）
	Summarizer Summarizer
}

// Trim 在预算内修剪消息列表。
//  1. 超预算时从最旧非 system 消息开始裁剪
//  2. 仍超预算且有 Summarizer → 把最旧一半压成一条摘要
func (w *ContextWindow) Trim(msgs []provider.Message) []provider.Message {
	if w == nil || w.MaxTokens <= 0 {
		return msgs
	}
	for total(msgs) > w.MaxTokens && len(msgs) > 1 {
		// 找到第一条可裁剪的非 system 消息
		i := 0
		for ; i < len(msgs); i++ {
			if msgs[i].Role != "system" {
				break
			}
		}
		if i >= len(msgs) {
			break
		}
		msgs = append(msgs[:i], msgs[i+1:]...)
	}

	if w.Summarizer != nil && total(msgs) > w.MaxTokens {
		// 压最旧一半为摘要（保留最新消息的完整性）
		half := len(msgs) / 2
		old := msgs[:half]
		keep := msgs[half:]
		if summary, err := w.Summarizer.Summarize(context.Background(), old); err == nil {
			keep = append([]provider.Message{
				{Role: "system", Content: "早期对话摘要：\n" + summary},
			}, keep...)
			msgs = keep
		}
	}
	return msgs
}

func total(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += MessageTokens(m)
	}
	return n
}
