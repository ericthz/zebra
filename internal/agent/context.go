// 上下文工程：token 估算、滑动窗口、对话摘要压缩。
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
//  2. 仍超预算且有 Summarizer → 把最旧一半压成一条摘要，压完【复查预算】，
//     仍超则继续迭代（保护摘要消息，只删其余旧消息）
//
// 不变量：最后一条消息（当前用户问题）永不被删除（修复：此前超预算
// 会一路删到 [sys,sys,user]，删掉的是用户问题，模型收到"只有指令没问题"）。
//
// ctx 向下透传给 Summarizer（取消/超时传播，修复"Background 丢取消"）。
func (w *ContextWindow) Trim(ctx context.Context, msgs []provider.Message) []provider.Message {
	if w == nil || w.MaxTokens <= 0 {
		return msgs
	}
	// 至少保留 2 条消息：给摘要器留"可压缩的旧消息 + 最新消息"（修复：
	// 此前一路删到预算内，摘要阶段永远不触发，属死代码）。
	// 裁剪范围只到倒数第二条，最后一条（当前用户问题）不可裁剪。
	for total(msgs) > w.MaxTokens && len(msgs) > 2 {
		// 找到第一条可裁剪的非 system 消息（不含最后一条用户问题）
		i := 0
		for ; i < len(msgs)-1; i++ {
			if msgs[i].Role != "system" {
				break
			}
		}
		if i >= len(msgs)-1 {
			break // 只剩 system 与最新问题，无可裁剪
		}
		msgs = append(msgs[:i], msgs[i+1:]...)
	}

	// 压最旧一半为摘要（保留最新消息的完整性）；压完复查预算，仍超则继续剪
	for w.Summarizer != nil && total(msgs) > w.MaxTokens && len(msgs) >= 2 {
		prevTotal := total(msgs)
		half := len(msgs) / 2
		if half < 1 {
			break // 无进展空间
		}
		old := msgs[:half]
		keep := msgs[half:]
		if summary, err := w.Summarizer.Summarize(ctx, old); err != nil {
			break // 摘要失败：放弃压缩，保留现状
		} else {
			keep = append([]provider.Message{
				{Role: "system", Content: "早期对话摘要：\n" + summary},
			}, keep...)
			msgs = keep
		}
		// 复查：摘要仍超预算且没有进一步压缩空间 → 停止（防死循环）
		if total(msgs) >= prevTotal {
			break
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
