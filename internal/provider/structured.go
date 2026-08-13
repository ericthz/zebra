// 结构化输出通用助手（P17）：优先"强约束"，回退"事后校验"。
package provider

import (
	"context"
	"fmt"

	"github.com/ericthz/zebra/internal/schema"
)

// StructuredProvider 可选接口：支持生成前强约束（response_format）的 provider。
type StructuredProvider interface {
	ChatJSON(ctx context.Context, messages []Message, jsonSchema map[string]interface{}) (Message, error)
}

// StructuredChat 请求结构化输出：
//   1. 若主 provider 支持强约束（ChatJSON）→ 用它（生成前就按 schema）
//   2. 否则走普通 Chat，再用 schema.Validate 事后校验
// 返回：回复文本 + 解析后的 JSON 值 + 错误。
func StructuredChat(ctx context.Context, router *Router, messages []Message, jsonSchema map[string]interface{}) ([]byte, error) {
	// 1. 强约束
	if sp, ok := router.Primary().(StructuredProvider); ok {
		msg, err := sp.ChatJSON(ctx, messages, jsonSchema)
		if err == nil && msg.Content != "" {
			if err := schema.Validate([]byte(msg.Content), jsonSchema); err == nil {
				return []byte(msg.Content), nil
			}
			// 强约束仍不符 → 落到普通路径
		}
	}
	// 2. 普通调用 + 事后校验
	msg, _, err := router.ChatWithFallback(ctx, messages, nil)
	if err != nil {
		return nil, err
	}
	if err := schema.Validate([]byte(msg.Content), jsonSchema); err != nil {
		return nil, fmt.Errorf("结构化输出校验失败: %w", err)
	}
	return []byte(msg.Content), nil
}
