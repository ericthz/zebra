// Package mcp 自研 MCP（Model Context Protocol）协议栈：JSON-RPC 2.0 + tools。
//
// 实现范围（对齐官方规范子集）：
//   - initialize / initialized 握手
//   - tools/list、tools/call
//   - stdio 与 HTTP 双传输层
// 生产演进方向：resources / prompts / sampling / notifications 能力协商。
package mcp

import "encoding/json"

// Request JSON-RPC 2.0 请求。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response JSON-RPC 2.0 响应。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error JSON-RPC 错误。
type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// 协议版本与能力协商结果。
const ProtocolVersion = "2024-11-05"

// InitializeResult initialize 响应。
type InitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    map[string]any  `json:"capabilities"`
	ServerInfo      map[string]any  `json:"serverInfo"`
}

// ToolDef MCP 工具定义。
type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ListToolsResult tools/list 结果。
type ListToolsResult struct {
	Tools []ToolDef `json:"tools"`
}

// CallToolParams tools/call 参数。
type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// CallToolResult tools/call 结果。
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock 工具结果内容块。
type ContentBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}
