// MCP 工具适配器：把远端 MCP 工具变成 tool.Tool，无缝并入 Agent。
package mcp

import (
	"context"

	"github.com/ericthz/zebra/internal/tool"
)

// MCPToolAdapter 实现 tool.Tool，底层通过 MCP 客户端调用远端工具。
type MCPToolAdapter struct {
	client *Client
	def    ToolDef
}

// NewMCPToolAdapter 构造适配器。
func NewMCPToolAdapter(client *Client, def ToolDef) *MCPToolAdapter {
	return &MCPToolAdapter{client: client, def: def}
}

func (a *MCPToolAdapter) Name() string        { return a.def.Name }
func (a *MCPToolAdapter) Description() string { return a.def.Description }
func (a *MCPToolAdapter) Parameters() map[string]interface{} {
	return a.def.InputSchema
}

// Execute 调用远端工具并合并文本内容块。
func (a *MCPToolAdapter) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	result, err := a.client.CallTool(ctx, a.def.Name, args)
	if err != nil {
		return "", err
	}
	var out string
	for _, b := range result.Content {
		if b.Type == "text" {
			out += b.Text
		}
	}
	if out == "" {
		out = "（工具无返回内容）"
	}
	return out, nil
}

// 编译期断言：适配器实现工具接口。
var _ tool.Tool = (*MCPToolAdapter)(nil)
