// main.go
// 完整的 AI Agent 入口，支持本地工具和 MCP 外部工具
//
// 使用方法：
//  1. 确保 Ollama 已启动，并拉取支持工具调用的模型（默认 llama3.1）
//  2. 可选：创建 .env 文件，配置 OLLAMA_BASE_URL、OLLAMA_MODEL、OLLAMA_PROVIDER
//  3. 启动 MCP 服务器（二选一）：
//     - stdio 模式：go run mcp_server.go（默认）
//     - HTTP 模式：go run mcp_server.go -http :9000
//  4. 运行主程序：go run .
//  5. 可以通过环境变量 MCP_MODE 指定 mcp 连接方式（stdio/http），
//     以及 MCP_COMMAND 指定启动命令（stdio 模式），MCP_HTTP_URL 指定 HTTP 地址。
//
// 工具覆盖规则：
//
//	先注册所有本地工具，再注册 MCP 工具。若 MCP 工具与本地工具同名，则 MCP 版本会覆盖本地版本。
//
// main.go
// 集成 MCP 工具的 AI Agent，MCP 后注册的同名工具将覆盖本地工具

package main

import (
	"log"
	"os"
	"strings"

	"github.com/ericthz/zebra/agent"
	"github.com/ericthz/zebra/config"
	"github.com/ericthz/zebra/mcp"
	"github.com/ericthz/zebra/provider"
	"github.com/ericthz/zebra/tool"
)

func main() {
	if err := config.LoadEnv(".env"); err != nil {
		log.Println("未找到 .env 文件，使用默认配置")
	}

	// Provider 配置
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "llama3.1"
	}
	providerName := strings.ToLower(os.Getenv("OLLAMA_PROVIDER"))
	if providerName == "" {
		providerName = "ollama"
	}
	apiKey := os.Getenv("OLLAMA_API_KEY")

	var prov provider.Provider
	switch providerName {
	case "ollama", "native":
		prov = &provider.OllamaNativeProvider{BaseURL: baseURL, Model: model}
	case "openai":
		prov = &provider.OllamaOpenAIProvider{BaseURL: baseURL, Model: model, APIKey: apiKey}
	case "anthropic":
		prov = &provider.OllamaAnthropicProvider{BaseURL: baseURL, Model: model, APIKey: apiKey}
	default:
		log.Fatalf("不支持的接口类型: %s", providerName)
	}
	log.Printf("🚀 使用接口: %s, 模型: %s, 地址: %s\n", providerName, model, baseURL)

	// 工具注册表
	reg := tool.NewRegistry()

	// 注册本地工具（可能被后续 MCP 工具覆盖）
	reg.Register(&tool.WeatherTool{})
	reg.Register(&tool.CalculatorTool{})
	reg.Register(&tool.DateTimeTool{})
	reg.Register(&tool.RandomTool{})
	reg.Register(&tool.SearchTool{})
	reg.Register(&tool.UnitConverterTool{})
	reg.Register(&tool.TranslateTool{})
	reg.Register(&tool.IPInfoTool{})

	// ---- MCP 集成 ----
	mcpMode := strings.ToLower(os.Getenv("MCP_MODE"))
	if mcpMode == "" {
		mcpMode = "stdio"
	}
	log.Printf("🔌 MCP 连接模式: %s\n", mcpMode)

	switch mcpMode {
	case "stdio":
		cmd := os.Getenv("MCP_COMMAND")
		if cmd == "" {
			cmd = "go run mcp_server.go"
		}
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			log.Println("⚠️ MCP_COMMAND 为空，跳过 MCP 集成")
			break
		}
		command := parts[0]
		args := parts[1:]
		transport, err := mcp.NewStdioClient(command, args...)
		if err != nil {
			log.Printf("⚠️ 无法启动 MCP stdio 服务器: %v", err)
		} else {
			mcpClient := mcp.NewClient(transport)
			mcpTools, err := mcpClient.ListTools()
			if err != nil {
				log.Printf("⚠️ 获取 MCP 工具列表失败: %v", err)
			} else {
				for _, def := range mcpTools {
					adapter := mcp.NewMCPToolAdapter(mcpClient, def)
					reg.Register(adapter) // 后注册，同名覆盖本地工具
					log.Printf("✅ 注册 MCP 工具 (stdio): %s", def.Name)
				}
			}
		}

	case "http":
		httpURL := os.Getenv("MCP_HTTP_URL")
		if httpURL == "" {
			httpURL = "http://localhost:9000"
		}
		transport := mcp.NewHTTPClient(httpURL)
		mcpClient := mcp.NewClient(transport)
		mcpTools, err := mcpClient.ListTools()
		if err != nil {
			log.Printf("⚠️ 连接 HTTP MCP 服务器失败: %v", err)
		} else {
			for _, def := range mcpTools {
				adapter := mcp.NewMCPToolAdapter(mcpClient, def)
				reg.Register(adapter)
				log.Printf("✅ 注册 MCP 工具 (HTTP): %s", def.Name)
			}
		}

	default:
		log.Printf("⚠️ 未知 MCP 模式: %s，跳过 MCP 集成", mcpMode)
	}

	// 启动 Agent
	ag := agent.NewAgent(prov, reg, 5)

	userInput := "北京今天天气怎么样？另外计算 15.6 * 3.2"
	log.Printf("👤 用户输入：%s\n", userInput)
	answer, err := ag.Run(userInput)
	if err != nil {
		log.Fatalf("❌ Agent 运行失败: %v", err)
	}
	log.Println("🤖 最终回答：")
	log.Println(answer)
}
