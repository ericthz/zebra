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
	httpToken := flag.String("http-token", os.Getenv("MCP_HTTP_TOKEN"), "HTTP 模式 Bearer 访问令牌（推荐设置，防未授权调用）")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// HTTP 模式默认拒绝——必须显式配置 Bearer 令牌才能启动，否则
	// 未授权调用可直达只读工具（消费配额、泄露出口 IP 等）。stdio 模式
	// 走本地管道无需鉴权（客户端已由启动方授权）。
	if err := mcp.ValidateHTTPStart(*httpAddr, *httpToken); err != nil {
		logger.Error("启动校验失败", "err", err)
		os.Exit(2)
	}

	srv := mcp.NewServer(logger)
	if *httpToken != "" {
		srv.SetHTTPToken(*httpToken)
		logger.Info("MCP HTTP 鉴权已启用")
	}

	// 注册全部内置只读工具为 MCP 工具（无本地沙箱，文档/命令类工具不放这里）
	// reg 作为执行权限层：只注册无副作用工具，Execute 走同一白名单。
	reg := tool.NewRegistry()
	safeTools := []tool.Tool{
		&tool.WeatherTool{}, &tool.CalculatorTool{}, &tool.DateTimeTool{}, &tool.RandomTool{},
		&tool.SearchTool{}, &tool.UnitConverterTool{}, &tool.TranslateTool{}, &tool.IPInfoTool{},
	}
	for _, t := range safeTools {
		reg.Register(t)
		srv.RegisterTool(mcp.ToolDef{
			Name: t.Name(), Description: t.Description(), InputSchema: t.Parameters(),
		}, func(ctx context.Context, args map[string]interface{}) (*mcp.CallToolResult, error) {
			// 直接执行（MCP 层只开放无副作用工具，鉴权由 HTTP token 负责）
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
