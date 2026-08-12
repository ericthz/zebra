// main.go
// 完整的 AI Agent 入口，支持多轮对话、上下文管理、语义记忆（Qdrant）、
// 本地工具和 MCP 外部工具。
//
// 使用方式：
//   1. 启动 Ollama 服务，拉取模型（默认 llama3.1）以及嵌入模型（默认 nomic-embed-text）
//   2. 确保 Qdrant 向量数据库已启动（默认 http://localhost:6333）
//   3. (可选) 启动 MCP 服务器（stdio 或 HTTP）
//   4. go run .
//   5. 交互式输入，输入 'exit' 退出，'clear' 清空历史

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/ericthz/aidemo/agent"
	"github.com/ericthz/aidemo/config"
	"github.com/ericthz/aidemo/mcp"
	"github.com/ericthz/aidemo/memory"
	"github.com/ericthz/aidemo/provider"
	"github.com/ericthz/aidemo/tool"
)

func main() {
	// 加载 .env 配置（可选）
	if err := config.LoadEnv(".env"); err != nil {
		log.Println("未找到 .env 文件，使用默认配置")
	}

	// ---------- Provider 配置 ----------
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
		log.Fatalf("不支持的接口类型: %s (支持 ollama, openai, anthropic)", providerName)
	}
	log.Printf("🚀 使用接口: %s, 模型: %s, 地址: %s\n", providerName, model, baseURL)

	// ---------- 工具注册表 ----------
	reg := tool.NewRegistry()

	// 注册本地工具
	reg.Register(&tool.WeatherTool{})       // 天气查询
	reg.Register(&tool.CalculatorTool{})    // 数学计算
	reg.Register(&tool.DateTimeTool{})      // 日期时间
	reg.Register(&tool.RandomTool{})        // 随机数
	reg.Register(&tool.SearchTool{})        // 网页搜索
	reg.Register(&tool.UnitConverterTool{}) // 单位换算
	reg.Register(&tool.TranslateTool{})     // 文本翻译
	reg.Register(&tool.IPInfoTool{})        // IP 信息查询

	// ---------- MCP 工具集成 ----------
	mcpMode := strings.ToLower(os.Getenv("MCP_MODE"))
	if mcpMode == "" {
		mcpMode = "stdio" // 默认使用 stdio 模式
	}
	log.Printf("🔌 MCP 连接模式: %s\n", mcpMode)

	switch mcpMode {
	case "stdio":
		// 从环境变量读取启动命令，默认运行 mcp_server.go
		cmd := os.Getenv("MCP_COMMAND")
		if cmd == "" {
			cmd = "go run mcp_server.go"
		}
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			log.Println("⚠️ MCP_COMMAND 为空，跳过 MCP 集成")
		} else {
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
						reg.Register(adapter) // 后注册的同名工具会覆盖本地实现
						log.Printf("✅ 注册 MCP 工具 (stdio): %s", def.Name)
					}
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

	// ---------- 记忆：使用 Qdrant 向量数据库 ----------
	var mem memory.Memory
	qdrantURL := os.Getenv("QDRANT_URL")
	if qdrantURL == "" {
		qdrantURL = "http://localhost:6333"
	}
	collectionName := os.Getenv("QDRANT_COLLECTION")
	if collectionName == "" {
		collectionName = "agent_memory"
	}
	embedModel := os.Getenv("EMBED_MODEL")
	if embedModel == "" {
		embedModel = "nomic-embed-text" // 默认嵌入模型
	}
	vectorSizeStr := os.Getenv("EMBED_VECTOR_SIZE")
	vectorSize := 768 // nomic-embed-text 默认 768 维
	if vectorSizeStr != "" {
		if size, err := strconv.Atoi(vectorSizeStr); err == nil {
			vectorSize = size
		}
	}

	// 嵌入服务地址：与 Ollama 共用，可通过 OLLAMA_BASE_URL 指定
	ollamaEmbedURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaEmbedURL == "" {
		ollamaEmbedURL = "http://localhost:11434"
	}

	// 初始化 Qdrant 记忆，嵌入提供者由环境变量 EMBED_PROVIDER 决定（ollama/openai）
	qmem := memory.NewQdrantMemory(qdrantURL, collectionName, ollamaEmbedURL, embedModel, vectorSize)

	// 快速检查记忆是否可用（尝试检索一个测试词）
	if _, err := qmem.Retrieve("test", 1); err != nil {
		log.Printf("⚠️ Qdrant 记忆初始化警告: %v (将禁用记忆功能)", err)
		mem = nil
	} else {
		mem = qmem
		log.Printf("✅ 使用 Qdrant 语义记忆，集合: %s, 嵌入模型: %s", collectionName, embedModel)
	}

	// ---------- 创建 Agent ----------
	var ag *agent.Agent
	if mem != nil {
		ag = agent.NewAgentWithMemory(prov, reg, 5, mem)
		log.Println("🧠 Agent 已启用长期记忆")
	} else {
		ag = agent.NewAgent(prov, reg, 5) // 无记忆回退
	}
	ag.SetSystemPrompt("你是一个乐于助人的AI助手，可以调用各种工具，并记住之前的对话内容。")

	fmt.Println("\n🤖 智能助手已启动 (输入 'exit' 退出, 'clear' 清空历史)")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n👤 你: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("再见！")
			return
		case "clear":
			ag.ClearHistory()
			// 可选：同时清空记忆
			if mem != nil {
				if err := mem.Clear(); err != nil {
					log.Printf("清空记忆失败: %v", err)
				} else {
					fmt.Println("🧹 长期记忆已清空")
				}
			}
			fmt.Println("🗑️  对话历史已清空")
			continue
		}

		answer, err := ag.Run(input)
		if err != nil {
			fmt.Printf("❌ 错误: %v\n", err)
			continue
		}
		fmt.Printf("🤖 助手: %s\n", answer)
	}
}
