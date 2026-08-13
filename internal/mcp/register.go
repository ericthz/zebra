// MCP 工具装配（P32）：按环境变量挂载远端 MCP 工具，供 cmd/server 与
// cmd/zebra 共用，保证两个入口对"外部工具"的行为一致。
//
// 环境变量（与 README §5.2 对齐）：
//
//	MCP_MODE          "stdio"（本地子进程）/ "http"（远端服务）；设置即启用
//	MCP_COMMAND       stdio 模式的可执行命令（含参数）
//	MCP_HTTP_URL      http 模式的远端地址（默认 http://localhost:9000）
//
// 连接失败只告警不阻断启动（B7 降级：缺外部依赖时本地能力照常可用）。
package mcp

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/tool"
)

// RegisterTools 挂载 MCP 远端工具，返回（模式, 已注册的工具定义）。
// 未配置 MCP_MODE 或连接失败时返回 nil 并告警；返回的 defs 供启动清单
// 像本地工具一样逐项展示子项（名称 + 描述）。
func RegisterTools(reg *tool.Registry, logger *slog.Logger) (string, []ToolDef) {
	mode := strings.ToLower(os.Getenv("MCP_MODE"))
	if mode == "" {
		return mode, nil
	}
	// 握手超时放宽到 10s：stdio 子进程常是 `go run`（如 cmd/mcp），首启编译
	// 可能数秒；http 模式下连接失败是即时拒绝，不受此超时影响。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var client *Client
	switch mode {
	case "stdio":
		cmd := os.Getenv("MCP_COMMAND")
		if cmd == "" {
			return mode, nil
		}
		parts := strings.Fields(cmd)
		tr, err := NewStdioClient(parts[0], parts[1:]...)
		if err != nil {
			logger.Warn("MCP stdio 启动失败", "err", err)
			return mode, nil
		}
		client = NewClient(tr)
	case "http":
		tr := NewHTTPClient(envOr("MCP_HTTP_URL", "http://localhost:9000"))
		client = NewClient(tr)
	default:
		logger.Warn("MCP_MODE 不支持，已忽略", "mode", mode)
		return mode, nil
	}

	if err := client.Initialize(ctx); err != nil { // 握手（官方规范）
		logger.Warn("MCP initialize 失败", "err", err)
		return mode, nil
	}
	defs, err := client.ListTools(ctx)
	if err != nil {
		logger.Warn("MCP tools/list 失败", "err", err)
		return mode, nil
	}
	for _, def := range defs {
		reg.Register(NewMCPToolAdapter(client, def))
		logger.Info("已注册 MCP 工具", "name", def.Name)
	}
	return mode, defs
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
