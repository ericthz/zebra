// cmd/mcp —— 独立 MCP 服务器（stdio / HTTP），把内置工具暴露给任意 MCP 客户端。
//
//	stdio：go run ./cmd/mcp
//	HTTP： go run ./cmd/mcp -http :9000
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"

	"github.com/ericthz/zebra/internal/mcp"
	"github.com/ericthz/zebra/internal/tool"
)

func main() {
	httpAddr := flag.String("http", "", "HTTP 监听地址，如 :9000；为空则走 stdio")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := mcp.NewServer(logger)

	// 注册全部内置工具为 MCP 工具
	reg := tool.NewRegistry()
	reg.Register(&tool.WeatherTool{})
	reg.Register(&tool.CalculatorTool{})
	reg.Register(&tool.DateTimeTool{})
	reg.Register(&tool.RandomTool{})
	reg.Register(&tool.SearchTool{})
	reg.Register(&tool.UnitConverterTool{})
	reg.Register(&tool.TranslateTool{})
	reg.Register(&tool.IPInfoTool{})

	for _, t := range []tool.Tool{
		&tool.WeatherTool{}, &tool.CalculatorTool{}, &tool.DateTimeTool{}, &tool.RandomTool{},
		&tool.SearchTool{}, &tool.UnitConverterTool{}, &tool.TranslateTool{}, &tool.IPInfoTool{},
	} {
		srv.RegisterTool(mcp.ToolDef{
			Name: t.Name(), Description: t.Description(), InputSchema: t.Parameters(),
		}, func(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
			// 工具权限白名单已在 reg 中，这里直接执行（MCP 层可再加鉴权）
			res, err := reg.Execute(ctx, t.Name(), args, "mcp", "admin", true)
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.ContentBlock{{Type: "text", Text: res}}}, nil
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *httpAddr != "" {
		logger.Info("MCP server (HTTP)", "addr", *httpAddr)
		if err := srv.ServeHTTP(ctx, *httpAddr); err != nil {
			logger.Error("mcp server exited", "err", err)
			os.Exit(1)
		}
	} else {
		logger.Info("MCP server (stdio) waiting...")
		if err := srv.ServeStdio(ctx); err != nil {
			logger.Error("mcp server exited", "err", err)
			os.Exit(1)
		}
	}
}
