// 结构化输出通用助手：优先"强约束"，回退"事后校验"。
package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ericthz/zebra/internal/schema"
)

// StructuredProvider 可选接口：支持生成前强约束（response_format）的 provider。
type StructuredProvider interface {
	ChatJSON(ctx context.Context, messages []Message, jsonSchema map[string]interface{}) (Message, error)
}

// StructuredChat 请求结构化输出：
//  1. 若主 provider 支持强约束（ChatJSON）→ 用它（生成前就按 schema）
//  2. 否则走普通 Chat，再用 schema.Validate 事后校验
//
// 返回：回复文本 + 解析后的 JSON 值 + 错误。
func StructuredChat(ctx context.Context, router *Router, messages []Message, jsonSchema map[string]interface{}) ([]byte, error) {
	// 1. 强约束
	if sp, ok := router.Primary().(StructuredProvider); ok {
		msg, err := sp.ChatJSON(ctx, messages, jsonSchema)
		if err == nil && msg.Content != "" {
			if err := schema.Validate([]byte(msg.Content), jsonSchema); err == nil {
				model := ""
				if p := router.Primary(); p != nil {
					model = p.Name()
				}
				ReportUsage(ctx, UsageCall{Model: model, In: messages, Out: msg})
				return []byte(msg.Content), nil
			}
			// 强约束仍不符 → 落到普通路径
		}
		// 主模型强约束失败（小模型常见）时不得再次调用主模型——
		// ChatWithFallback 会从链首重新尝试，导致同一主模型被调两次（成本
		// 翻倍）。改为从"主模型之后的备选"开始普通调用，避免重复请求。
		return structuredFallback(ctx, router, messages, jsonSchema, router.Primary())
	}
	// 2. 普通调用 + 事后校验
	msg, _, err := router.ChatWithFallback(ctx, messages, nil)
	if err != nil {
		return nil, err
	}
	content := msg.Content
	if err := schema.Validate([]byte(content), jsonSchema); err != nil {
		// 小模型常把 JSON 包在 Markdown 代码围栏里（```json ... ```），
		// 直接校验失败；提取 JSON 块再校验（修复，惠及全部结构化输出）。
		if extracted := extractJSON(content); extracted != "" {
			if schema.Validate([]byte(extracted), jsonSchema) == nil {
				return []byte(extracted), nil
			}
			// 单键包裹兼容：{"plan":{...}} / {"result":{...}} 等（小模型常见）
			if inner := unwrapWrapper(extracted); inner != "" && schema.Validate([]byte(inner), jsonSchema) == nil {
				return []byte(inner), nil
			}
			// 顶层数组兼容：规划直接输出成步骤数组等（见 fixArrayOutput）
			if fixed := fixArrayOutput(extracted, jsonSchema); fixed != "" {
				return []byte(fixed), nil
			}
		}
		return nil, fmt.Errorf("结构化输出校验失败: %w（原始输出: %s）", err, truncate(content, 240))
	}
	return []byte(content), nil
}

// structuredFallback 强约束失败后的回退路径：跳过主模型，从链上其余候选
// 依次普通调用 + 事后校验（不再让 ChatWithFallback 从头重试主模型）。
// skip 指定要跳过的 provider（刚失败的主模型，可能为 nil）。
func structuredFallback(ctx context.Context, router *Router, messages []Message, jsonSchema map[string]interface{}, skip Provider) ([]byte, error) {
	for _, p := range router.Chain() {
		if p == nil || p == skip {
			continue
		}
		msg, err := p.Chat(ctx, messages, nil)
		if err != nil {
			continue // 该候选失败 → 下一个
		}
		if out, ok := validateStructured(msg.Content, jsonSchema); ok {
			ReportUsage(ctx, UsageCall{Model: p.Name(), In: messages, Out: msg})
			return out, nil
		}
	}
	// 全部候选均失败：最后再给主模型一次普通调用机会（比静默失败好），
	// 但只有它自己是唯一候选时才发生，不会造成翻倍调用。
	if skip != nil {
		if msg, err := skip.Chat(ctx, messages, nil); err == nil {
			if out, ok := validateStructured(msg.Content, jsonSchema); ok {
				ReportUsage(ctx, UsageCall{Model: skip.Name(), In: messages, Out: msg})
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("结构化输出校验失败: 无可用候选产出合法 JSON（原始 schema: %v）", jsonSchema["type"])
}

// validateStructured 校验并尽力提取合法 JSON；失败返回 ("", false)。
func validateStructured(content string, jsonSchema map[string]interface{}) ([]byte, bool) {
	if err := schema.Validate([]byte(content), jsonSchema); err == nil {
		return []byte(content), true
	}
	if extracted := extractJSON(content); extracted != "" {
		if schema.Validate([]byte(extracted), jsonSchema) == nil {
			return []byte(extracted), true
		}
		if inner := unwrapWrapper(extracted); inner != "" && schema.Validate([]byte(inner), jsonSchema) == nil {
			return []byte(inner), true
		}
		if fixed := fixArrayOutput(extracted, jsonSchema); fixed != "" {
			return []byte(fixed), true
		}
	}
	return nil, false
}

// truncate 截断用于错误信息的长文本，避免刷屏。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// unwrapWrapper 若对象只有单个键（如 {"plan":X}），返回其值（X）。
func unwrapWrapper(s string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &m) != nil || len(m) != 1 {
		return ""
	}
	for _, v := range m {
		return string(v)
	}
	return ""
}

// fixArrayOutput 顶层数组容错（小模型常把规划对象直接输出成数组）：
//  1. 数组只有 1 个元素且元素本身是目标对象 → 解包
//  2. 目标 schema 顶层含数组属性 steps → 包装成 {"steps":[...]}
func fixArrayOutput(s string, jsonSchema map[string]interface{}) string {
	if len(s) == 0 || s[0] != '[' || !json.Valid([]byte(s)) {
		return ""
	}
	var arr []json.RawMessage
	if json.Unmarshal([]byte(s), &arr) == nil && len(arr) == 1 {
		if schema.Validate(arr[0], jsonSchema) == nil {
			return string(arr[0])
		}
	}
	props, _ := jsonSchema["properties"].(map[string]interface{})
	steps, _ := props["steps"].(map[string]interface{})
	if typ, _ := steps["type"].(string); typ == "array" {
		wrapped := `{"steps":` + s + `}`
		if schema.Validate([]byte(wrapped), jsonSchema) == nil {
			return wrapped
		}
	}
	return ""
}

// extractJSON 从模型回复中稳健提取 JSON：
//  1. 找第一个顶层值起点（对象 { 或数组 [，跳过前置解释文本/代码围栏）
//  2. 按括号配对（跳过字符串字面量）取第一个完整顶层值
func extractJSON(s string) string {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	open, close := byte('{'), byte('}')
	if s[start] == '[' {
		open, close = '[', ']'
	}
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
