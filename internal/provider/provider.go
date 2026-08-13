// Package provider 统一 LLM 协议适配层。
//
// 设计要点：
//   - 接口驱动：Chat / ChatStream 两种调用方式，上层不感知具体厂商协议
//   - 全部带 ctx：支持超时、取消、链路传播（B9 错误恢复）
//   - Message 支持多模态内容块（C14 多模态）
package provider

import (
	"context"
	"encoding/json"
)

// Part 多模态内容块（C14）
// Type 取值："text" | "image_url"，有 ContentParts 时优先于 Message.Content。
type Part struct {
	Type     string `json:"type"`                // "text" | "image_url"
	Text     string `json:"text,omitempty"`      // text 内容
	ImageURL string `json:"image_url,omitempty"` // 图片地址（data: 或 http(s)）
}

// Message 统一的对话消息模型（兼容三大协议的字段超集）。
type Message struct {
	Role         string     `json:"role"`
	Content      string     `json:"content,omitempty"`
	ContentParts []Part     `json:"content_parts,omitempty"` // 多模态
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	Name         string     `json:"name,omitempty"`
}

// ToolCall 模型请求的工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 工具名与参数（arguments 保留原始 JSON，交给工具层解析）。
type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Tool 描述一个工具的 schema，发送给模型的"工具说明书"。
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef 工具定义（JSON Schema 风格的 parameters）。
type FunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// StreamEventType 流式事件类型（C10）。
type StreamEventType string

const (
	StreamEventDelta StreamEventType = "delta" // 文本增量
	StreamEventTool  StreamEventType = "tool"  // 模型请求调用工具
	StreamEventDone  StreamEventType = "done"  // 本轮完成
	StreamEventError StreamEventType = "error" // 出错
)

// StreamEvent 流式事件。
type StreamEvent struct {
	Type     StreamEventType `json:"type"`
	Content  string          `json:"content,omitempty"`
	ToolCall *ToolCall       `json:"tool_call,omitempty"`
	Err      error           `json:"-"`
}

// Provider 统一 LLM 调用接口。
//
//	Chat        非流式（完整回复一次性返回）
//	ChatStream  流式（增量文本/工具调用事件）；实现方若底层不支持流式，
//	           可回退为"调用 Chat 后发一个 delta + done"（见 anthropic.go）
type Provider interface {
	Name() string
	Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error)
	ChatStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamEvent, error)
}

// TextParts 便捷构造：把纯文本转为单块多模态内容（供 OpenAI 兼容层复用）。
func TextParts(text string) []Part {
	return []Part{{Type: "text", Text: text}}
}
