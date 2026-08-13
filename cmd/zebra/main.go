// cmd/zebra —— 单机 CLI 版 Agent（学习入口，企业能力走 cmd/server）。
//
// 用法：
//
//	go run ./cmd/zebra
//	go run ./cmd/server          # 企业版 HTTP 服务
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/config"
	"github.com/ericthz/zebra/internal/console"
	"github.com/ericthz/zebra/internal/mcp"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/observe"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

func main() {
	// ---- P37 终端 banner ----
	observe.PrintBanner(os.Stdout, "zebra CLI — AI Agent 单机学习入口（输入 exit 退出）")

	// ---- P30 配置加载：先读 .env（决定日志去向与全部配置）----
	envN, envErr := config.LoadDefault()

	// ---- P34 诊断日志落盘：终端保持干净，依赖探测细节写日志文件 ----
	// ZEBRA_LOG 指定路径（默认 zebra.log）；ZEBRA_LOG=off 退回 stderr。
	// MCP/Qdrant 等可选依赖的降级原因不再刷屏，进日志文件便于排查。
	logWriter, closeLog, logErr := config.OpenLogFile(envOr("ZEBRA_LOG", "zebra.log"))
	if logErr != nil || logWriter == nil {
		logWriter, closeLog = os.Stderr, func() {}
		if logErr != nil {
			fmt.Fprintf(os.Stderr, "打开日志文件失败，诊断日志将输出到 stderr: %v\n", logErr)
		}
	}
	defer closeLog()

	logger := slog.New(slog.NewTextHandler(logWriter, nil))
	slog.SetDefault(logger)

	if envErr != nil {
		logger.Warn("加载 .env 失败，继续使用系统环境变量/默认值", "err", envErr)
	} else if envN > 0 {
		logger.Info("已从 .env 加载配置", "count", envN)
	}

	// ---- LLM（默认 Ollama 原生，流式）----
	httpCli := provider.NewHTTPClient(20*time.Second, 2, 300*time.Millisecond)
	router := provider.NewRouter(&provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
		Client:  httpCli,
	})

	// ---- 工具（zebra CLI 全量开放）----
	reg := tool.NewRegistry()
	reg.Register(&tool.WeatherTool{})
	reg.Register(&tool.CalculatorTool{})
	reg.Register(&tool.DateTimeTool{})
	reg.Register(&tool.RandomTool{})
	reg.Register(&tool.SearchTool{})
	reg.Register(&tool.UnitConverterTool{})
	reg.Register(&tool.TranslateTool{})
	reg.Register(&tool.IPInfoTool{})

	reg.Register(&tool.FetchURLTool{}) // P6 SSRF 防护的抓取工具
	// ---- P2 本地执行：文件读写 + 命令执行（zebra CLI 默认可写，便于演示 Agentic 能力）----
	execSandbox := tool.NewExecSandbox("workspace", false)
	reg.Register(&tool.ListDirTool{Sandbox: execSandbox})
	reg.Register(&tool.ReadFileTool{Sandbox: execSandbox})
	reg.Register(&tool.WriteFileTool{Sandbox: execSandbox})
	reg.Register(&tool.RunCommandTool{Sandbox: execSandbox})
	// ---- P32 MCP 远端工具：与 cmd/server 同一装配（MCP_MODE 设置即启用）----
	mcpMode, mcpCount := mcp.RegisterTools(reg, logger)

	// ---- P1 技能体系：加载 skills/ 目录 ----
	skillReg := skill.NewRegistry()
	if loaded, err := skill.LoadDir("skills"); err == nil {
		skillReg.LoadAll(loaded)
	}
	skills := skillReg.List()

	// ---- P32 记忆：与 cmd/server 同一装配（QDRANT_URL 设置且可用则启用长期记忆）----
	mem, longMem := memory.SetupManager(logger)

	// ---- P36 知识库（RAG）：与 cmd/server 同一装配，加载 docs/ 目录 ----
	var ragIndex *rag.Index
	docsCount := 0
	if emb := memory.NewEmbedderFromEnv(); emb != nil {
		ragIndex = rag.NewIndex(emb)
		if docs, err := rag.LoadDocs("docs"); err == nil && len(docs) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			for name, content := range docs {
				if derr := ragIndex.AddDocument(ctx, content, name, 600, 100); derr != nil {
					logger.Warn("RAG 文档摄入失败", "doc", name, "err", derr)
					continue
				}
				docsCount++
			}
			cancel()
		}
	}

	// ---- P36 语音客户端：与 cmd/server 同一装配（VOICE_BASE_URL 设置即启用）----
	var voice *provider.VoiceClient
	if vb := os.Getenv("VOICE_BASE_URL"); vb != "" {
		voice = &provider.VoiceClient{
			BaseURL:  vb,
			APIKey:   os.Getenv("VOICE_API_KEY"),
			ASRModel: envOr("VOICE_ASR_MODEL", "whisper-1"),
			TTSModel: envOr("VOICE_TTS_MODEL", "tts-1"),
			Voice:    envOr("VOICE_TONE", "alloy"),
			Client:   &http.Client{Timeout: time.Duration(atoiDefault(os.Getenv("HTTP_TIMEOUT"), 60)) * time.Second},
		}
	}

	// ---- 系统提示模板 ----
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: `你是 zebra AI 助手，可以调用工具。回答简洁准确。角色：{role}。`})

	ag := agent.New(agent.Config{
		Router:     router,
		Tools:      reg,
		Prompts:    prompts,
		Mem:        mem,
		Window:     &agent.ContextWindow{MaxTokens: 4000, Summarizer: agent.PrefixSummarizer{MaxChars: 600}},
		Moderator:  safety.NewKeywordModerator(),
		MaxTurns:   5,
		PromptName: "assistant",
		Skills:     skillReg,
		RAG:        ragIndex,
		// P31 执行痕迹：工具调用与技能注入在终端可见，学习 Agent 行为
		OnTool: func(name string, args map[string]interface{}, ok bool, err error) {
			detail, _ := json.Marshal(args)
			sym := console.Symbol("▲", console.ColorTool)
			if ok {
				fmt.Printf("  %s 工具调用: %s(%s) ✓\n", sym, name, detail)
			} else {
				fmt.Printf("  %s 工具调用: %s(%s) ✗ %v\n", sym, name, detail, err)
			}
		},
		OnSkill: func(names []string) {
			fmt.Printf("  %s 技能注入: %s\n", console.Symbol("■", console.ColorSkill), strings.Join(names, ", "))
		},
	})

	history := make([]provider.Message, 0)
	ag.Bind("zebra", "admin", "local", &history)

	// ---- P31/P36 启动清单：与 cmd/server 同一渲染（行结构/符号/配色一致）----
	memMode := "工作记忆"
	if longMem {
		memMode = "工作记忆 + Qdrant"
	}
	ragChunks := 0
	if ragIndex != nil {
		ragChunks = ragIndex.Len()
	}
	observe.PrintInventory(os.Stdout, observe.Info{
		Title:        "zebra CLI Agent",
		Models:       []string{router.Primary().Name()},
		Tools:        reg,
		Skills:       skills,
		MCPMode:      mcpMode,
		MCPCount:     mcpCount,
		MemMode:      memMode,
		RAGDocs:      docsCount,
		RAGChunks:    ragChunks,
		VoiceEnabled: voice != nil,
	})
	fmt.Println(strings.Repeat("─", 60))
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(console.Symbol(">", console.ColorTitle) + " ")
		if !sc.Scan() {
			break
		}
		in := strings.TrimSpace(sc.Text())
		if in == "" {
			continue
		}
		if in == "exit" {
			break
		}
		ctx := context.Background()
		answer, err := ag.Run(ctx, in, agent.RunOptions{})
		if err != nil {
			fmt.Printf("✗ %v\n", err)
			continue
		}
		fmt.Println(console.Symbol("»", console.ColorModel), answer)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// atoiDefault 字符串转 int，失败返回默认值。
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
