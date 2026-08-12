package mcp

import (
	"fmt"
)

/*
	功能总结
	功能					实现情况
	MCP 客户端 (stdio)		✅ 启动子进程，发送 JSON‑RPC 请求
	MCP 客户端 (HTTP)		✅ POST 请求
	tools/list 请求/响应	✅ 完整支持
	tools/call 请求/响应	✅ 完整支持
	MCP 服务器框架			✅ 注册工具处理器，支持 stdio / HTTP 两种模式
	适配器				    ✅ 将 MCP 工具转为 tool.Tool，无缝集成到现有 Agent
	多工具同时注册		     ✅ 可同时使用本地工具和多个 MCP 服务器工具
*/

// 将 MCP 工具适配为 tool.Tool 接口

// MCPToolAdapter 实现 tool.Tool 接口，通过 MCP 客户端调用远程工具
type MCPToolAdapter struct {
	client *Client
	def    ToolDef
}

func NewMCPToolAdapter(client *Client, def ToolDef) *MCPToolAdapter {
	return &MCPToolAdapter{client: client, def: def}
}

func (t *MCPToolAdapter) Name() string {
	return t.def.Name
}

func (t *MCPToolAdapter) Description() string {
	return t.def.Description
}

func (t *MCPToolAdapter) Parameters() map[string]interface{} {
	return t.def.InputSchema
}

func (t *MCPToolAdapter) Execute(args map[string]interface{}) (string, error) {
	result, err := t.client.CallTool(t.def.Name, args)
	if err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from MCP tool")
	}
	// 合并所有 content blocks 的文本
	var output string
	for _, block := range result.Content {
		if block.Type == "text" {
			output += block.Text
		}
	}
	if result.IsError {
		return output, fmt.Errorf(output)
	}
	return output, nil
}
