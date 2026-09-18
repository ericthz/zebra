// cmd/zebra —— 本地命令行 Agent 客户端（交互式终端；服务端能力走 cmd/server）。
//
// 用法：
//
//	go run ./cmd/zebra
//	go run ./cmd/server          # 服务端 HTTP 服务
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
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
	"github.com/ericthz/zebra/internal/plugin"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/redis"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/supervisor"
	"github.com/ericthz/zebra/internal/tool"
)

func main() {
	// ---- 启动模式：与 Web UI 的模式选择一致 ----
	mode := flag.String("mode", "chat", "启动模式: chat|plan|react|reflect|debate|supervisor|consistent")
	// 多模态：启动时可带图片（http(s) URL / data: 数据 URI / 本地文件路径），
	// 每轮对话都附带上该图；可重复传多张，换图需重启（CLI 简化；生产用多轮上传）。
	var images multiFlag
	flag.Var(&images, "image", "多模态图片: http(s) URL | data: 数据 URI | 本地文件路径（可重复）")
	flag.Parse()
	// ReAct 最大推理-行动步数（默认 6；REACT_MAX_STEPS 可调）
	reactMaxSteps := atoiDefault(os.Getenv("REACT_MAX_STEPS"), 6)

	// ---- 终端 banner ----
	observe.PrintBanner(os.Stdout, "Zebra CLI — 本地命令行 Agent 客户端（输入 exit 退出）")

	// ---- 配置加载：先读 .env（决定日志去向与全部配置）----
	envN, envErr := config.LoadDefault()

	// ---- 诊断日志落盘：终端保持干净，依赖探测细节写日志文件 ----
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
	// HTTP_TIMEOUT / HTTP_RETRIES / HTTP_BACKOFF_MS / CIRCUIT_THRESHOLD /
	// CIRCUIT_COOLDOWN_SEC 可调（本地大模型首 token 慢，默认 60s）
	httpCli := provider.NewHTTPClientWithBreaker(
		time.Duration(atoiDefault(os.Getenv("HTTP_TIMEOUT"), 60))*time.Second,
		atoiDefault(os.Getenv("HTTP_RETRIES"), 2),
		time.Duration(atoiDefault(os.Getenv("HTTP_BACKOFF_MS"), 300))*time.Millisecond,
		atoiDefault(os.Getenv("CIRCUIT_THRESHOLD"), 5),
		time.Duration(atoiDefault(os.Getenv("CIRCUIT_COOLDOWN_SEC"), 30))*time.Second,
	)
	router := provider.NewRouter(&provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
		Client:  httpCli,
	})

	// ---- 工具（Zebra CLI 全量开放）----
	reg := tool.NewRegistry()
	reg.Register(&tool.WeatherTool{})
	reg.Register(&tool.CalculatorTool{})
	reg.Register(&tool.DateTimeTool{})
	reg.Register(&tool.RandomTool{})
	reg.Register(&tool.SearchTool{})
	reg.Register(&tool.UnitConverterTool{})
	reg.Register(&tool.TranslateTool{})
	reg.Register(&tool.IPInfoTool{})

	reg.Register(&tool.FetchURLTool{Moderator: safety.NewKeywordModerator()}) // SSRF 防护 + 内容审核
	// ---- 本地执行：文件读写 + 命令执行（Zebra CLI 默认可写，便于演示 Agentic 能力）----
	execSandbox := tool.NewExecSandbox("workspace", false)
	reg.Register(&tool.ListDirTool{Sandbox: execSandbox})
	reg.Register(&tool.ReadFileTool{Sandbox: execSandbox})
	reg.Register(&tool.WriteFileTool{Sandbox: execSandbox})
	reg.Register(&tool.RunCommandTool{Sandbox: execSandbox})
	// ---- MCP 远端工具：与 cmd/server 同一装配（MCP_MODE 设置即启用）----
	mcpMode, mcpDefs := mcp.RegisterTools(reg, logger)
	// ---- 插件动态加载：plugins/ 目录 JSON 定义的外部 HTTP 工具 ----
	if _, err := os.Stat("plugins"); err == nil {
		if defs, lerr := plugin.Load("plugins"); lerr == nil && len(defs) > 0 {
			plugin.Register(reg, defs)
		}
	}

	// ---- 技能体系：加载 skills/ 目录 ----
	skillReg := skill.NewRegistry()
	if loaded, err := skill.LoadDir("skills"); err == nil {
		skillReg.LoadAll(loaded)
	}
	skills := skillReg.List()

	// ---- 记忆：与 cmd/server 同一装配（QDRANT_URL 设置且可用则启用长期记忆）----
	mem, longMem := memory.SetupManager(logger)
	// Redis 长期记忆：无 Qdrant 但配了 REDIS_URL 时启用（关键词检索）——
	// 与 cmd/server 行为对齐，避免 CLI 配了 Redis 但长期记忆不生效。
	if !longMem {
		if rurl := os.Getenv("REDIS_URL"); rurl != "" {
			rc := &redis.Client{
				Addr:     rurl,
				Password: os.Getenv("REDIS_PASSWORD"),
				DB:       atoiDefault(os.Getenv("REDIS_DB"), 0),
			}
			if rm, ok := memory.SetupManagerRedis(rc, logger); ok {
				mem = rm
				longMem = true
			}
		}
	}

	// ---- 知识库（RAG）：与 cmd/server 同一装配，加载 docs/ 目录 ----
	var ragIndex *rag.Index
	docsCount := 0
	if emb := memory.NewEmbedderFromEnv(); emb != nil {
		ragIndex = rag.NewIndex(emb)
		if docs, err := rag.LoadDocs("docs"); err == nil && len(docs) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			for name, content := range docs {
				// RAG_CHUNK_SIZE / RAG_CHUNK_OVERLAP 文档分块参数（默认 600/100）
				if derr := ragIndex.AddDocument(ctx, content, name, atoiDefault(os.Getenv("RAG_CHUNK_SIZE"), 600), atoiDefault(os.Getenv("RAG_CHUNK_OVERLAP"), 100)); derr != nil {
					logger.Warn("RAG 文档摄入失败", "doc", name, "err", derr)
					continue
				}
				docsCount++
			}
			cancel()
		}
	}

	// ---- 语音客户端：与 cmd/server 同一装配（VOICE_BASE_URL 设置即启用）----
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
	prompts.Register(&prompt.Template{Name: "data", Version: "v1", Text: `你是 zebra 的【数据专家 Agent】。擅长计算、单位换算、翻译、日期时间等数据处理任务。调用合适的工具得出准确结果，回答简洁。角色：{role}。`})
	prompts.Register(&prompt.Template{Name: "knowledge", Version: "v1", Text: `你是 zebra 的【知识专家 Agent】。擅长搜索资料、抓取网页、查阅文档等知识获取任务。调用合适的工具，基于事实回答并注明来源。角色：{role}。`})

	// 执行痕迹：工具调用与技能注入在终端可见，学习 Agent 行为（worker 复用同一钩子）
	toolHook, skillHook := traceHooks()

	ag := agent.New(agent.Config{
		Router:  router,
		Tools:   reg,
		Prompts: prompts,
		Mem:     mem,
		// CONTEXT_MAX_TOKENS 上下文预算（默认 4000）；SUMMARY_MAX_CHARS 摘要长度（默认 600）
		Window:    &agent.ContextWindow{MaxTokens: atoiDefault(os.Getenv("CONTEXT_MAX_TOKENS"), 4000), Summarizer: agent.PrefixSummarizer{MaxChars: atoiDefault(os.Getenv("SUMMARY_MAX_CHARS"), 600)}},
		Moderator: safety.NewKeywordModerator(),
		// MAX_TOOL_TURNS 最大工具轮数（默认 5）
		MaxTurns:     atoiDefault(os.Getenv("MAX_TOOL_TURNS"), 5),
		PromptName:   "assistant",
		Skills:       skillReg,
		RAG:          ragIndex,
		RewriteQuery: os.Getenv("ZEBRA_QUERY_REWRITE") == "1", // 查询改写
		OnTool:       toolHook,
		OnSkill:      skillHook,
	})

	history := make([]provider.Message, 0)
	ag.Bind("zebra", "admin", "local", &history)

	// ---- 多 Agent Supervisor：数据/知识/常规 三个专业 worker（与 cmd/server 同构）----
	// Zebra 单机版同样装配，让 /mode supervisor 与 Web UI 行为一致。
	sup := supervisor.NewSupervisor(router,
		&supervisor.Worker{
			Name:        "data",
			Description: "擅长计算、单位换算、翻译、日期时间等数据处理任务",
			Build:       workerBuilder(router, prompts, mem, skillReg, ragIndex, "data", reg.Subset("calculator", "get_current_datetime", "generate_random_number", "convert_units", "translate_text")),
		},
		&supervisor.Worker{
			Name:        "knowledge",
			Description: "擅长搜索资料、抓取网页、查阅文档等知识获取任务",
			Build:       workerBuilder(router, prompts, mem, skillReg, ragIndex, "knowledge", reg.Subset("web_search", "fetch_url", "read_file", "list_dir")),
		},
		&supervisor.Worker{
			Name:        "general",
			Description: "通用助手，擅长综合问答、文件操作、代码等一般任务",
			Build:       workerBuilder(router, prompts, mem, skillReg, ragIndex, "assistant", reg.Subset()),
		},
	)

	// ---- 启动清单：与 cmd/server 同一渲染（行结构/符号/配色一致）----
	memMode := "工作记忆"
	if longMem {
		memMode = "工作记忆 + Qdrant"
	}
	ragChunks := 0
	if ragIndex != nil {
		ragChunks = ragIndex.Len()
	}
	observe.PrintInventory(os.Stdout, observe.Info{
		Title:        "Zebra CLI Agent",
		Models:       []string{envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx")},
		Tools:        reg,
		Skills:       skills,
		MCPMode:      mcpMode,
		MCPCount:     len(mcpDefs),
		MCPTools:     observe.FromMCP(mcpDefs),
		MemMode:      memMode,
		RAGDocs:      docsCount,
		RAGChunks:    ragChunks,
		VoiceEnabled: voice != nil,
		Mode:         modeLabel(*mode) + "（输入 /mode 切换，/modes 查看全部）",
		Compact:      true, // 工具/MCP/技能只显示个数，子项用 /tools /mcp /skills 查看
		REPL:         true,
	})

	// ---- 多模态：把 -image 参数归一化为 provider 可消费的 image_url ----
	// 本地文件转 base64 data URI；URL/data: URI 原样透传。读取失败直接退出，
	// 避免"图没进去"的静默半实现（字段有、模型没收到图）。
	images, err := resolveImages(images)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(images) > 0 {
		fmt.Printf("  %s 已加载 %d 张图片，每轮对话附带（换图需重启）\n",
			console.Symbol("◉", console.ColorModel), len(images))
	}
	fmt.Println(strings.Repeat("─", 60))
	// 聊天历史：↑/↓ 在历史记录间选择（退出/EOF 时保留进程内历史）
	var chatHistory []string
	for {
		// raw 模式 + UTF-8 感知行编辑（中文退格不再残留字节残片）；
		// 输入 / 前缀实时提示命令补全，↑/↓ 选择历史记录。非 TTY 回退标准行读取。
		in, err := console.ReadLineFull(console.Symbol(">", console.ColorTitle)+" ", replCommands(), chatHistory)
		if err != nil {
			break // EOF / Ctrl-C / 中断
		}
		in = strings.TrimSpace(in)
		if in == "" {
			continue
		}
		// 追加历史（跳过空白；与上一条相同则不入，避免 ↑ 连续相同）
		if len(chatHistory) == 0 || chatHistory[len(chatHistory)-1] != in {
			chatHistory = append(chatHistory, in)
		}
		if in == "exit" {
			break
		}
		// ---- REPL 命令：模式 / 清单 / 环境变量 ----
		switch {
		case in == "/help" || in == "help" || in == "?":
			printHelp()
			continue
		case in == "/modes":
			printModes()
			continue
		case in == "/mode":
			fmt.Printf("  当前模式: %s\n", modeLabel(*mode))
			continue
		case strings.HasPrefix(in, "/mode "):
			name := strings.TrimSpace(strings.TrimPrefix(in, "/mode "))
			if !validMode(name) {
				fmt.Printf("  未知模式: %s（/modes 查看全部）\n", name)
				continue
			}
			*mode = name
			fmt.Printf("  已切换模式: %s\n", modeLabel(*mode))
			continue
		case in == "/env":
			observe.PrintEnv(os.Stdout, observe.EnvSpecs())
			continue
		case in == "/tools":
			observe.PrintToolDetails(os.Stdout, reg, false)
			continue
		case in == "/mcp":
			if mcpMode == "" {
				fmt.Println("  ● MCP: 未启用（MCP_MODE 未设置）")
			} else {
				fmt.Printf("  ● MCP: 模式=%s · 已连接 %d 个工具\n", mcpMode, len(mcpDefs))
				observe.PrintMCPDetails(os.Stdout, observe.FromMCP(mcpDefs), false)
			}
			continue
		case in == "/skills":
			observe.PrintSkillDetails(os.Stdout, skills, false)
			continue
		case in == "/model":
			fmt.Printf("  ◆ 模型: %s\n", envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"))
			if f := os.Getenv("FALLBACK_MODEL"); f != "" {
				fmt.Printf("  ◆ 备选: %s（故障自动降级）\n", f)
			}
			continue
		case in == "/provider":
			chain := router.Chain()
			labels := make([]string, 0, len(chain))
			for _, p := range chain {
				if p != nil {
					labels = append(labels, p.Name())
				}
			}
			fmt.Printf("  ◆ 供应商: %s\n", strings.Join(labels, " → "))
			continue
		case in == "/mem":
			fmt.Printf("  ▣ 记忆: %s\n", memMode)
			continue
		case in == "/rag":
			fmt.Printf("  ▤ 知识库: %d 篇文档 / %d 块\n", docsCount, ragChunks)
			continue
		case in == "/voice":
			if voice != nil {
				fmt.Println("  ♪ 语音: 已启用（ASR/TTS）")
			} else {
				fmt.Println("  ♪ 语音: 未启用（VOICE_BASE_URL 未设置）")
			}
			continue
		case in == "/image":
			if len(images) == 0 {
				fmt.Println("  ◉ 图片: 未加载（启动时 -image 附带）")
			} else {
				fmt.Printf("  ◉ 图片: %d 张（每轮对话附带）\n", len(images))
				for i, img := range images {
					short := img
					if len(short) > 60 {
						short = short[:60] + "…"
					}
					fmt.Printf("    %d. %s\n", i+1, short)
				}
			}
			continue
		case in == "/caps":
			observe.PrintInventory(os.Stdout, observe.Info{
				Title:        "能力清单（完整）",
				Models:       []string{envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx")},
				Tools:        reg,
				Skills:       skills,
				MCPMode:      mcpMode,
				MCPCount:     len(mcpDefs),
				MCPTools:     observe.FromMCP(mcpDefs),
				MemMode:      memMode,
				RAGDocs:      docsCount,
				RAGChunks:    ragChunks,
				VoiceEnabled: voice != nil,
				Mode:         modeLabel(*mode),
			})
			continue
		}
		ctx := context.Background()
		answer, err := runAgent(ctx, ag, sup, &history, *mode, reactMaxSteps, images, in)
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

// multiFlag 可重复的字符串 flag（如 -image a.png -image b.jpg）。
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// resolveImages 把 CLI 的图片参数归一化为 provider 可消费的 image_url：
//   - http(s):// 原样透传（模型侧抓取）
//   - data: 数据 URI 原样透传
//   - 其余按本地文件路径读取，转 base64 data URI（OpenAI 兼容 image_url 格式）
//     读取失败返回错误，避免静默丢图（半实现陷阱：字段有但图没进去）。
func resolveImages(in []string) ([]string, error) {
	var out []string
	for _, s := range in {
		switch {
		case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"),
			strings.HasPrefix(s, "data:"):
			out = append(out, s)
		default:
			b, err := os.ReadFile(s)
			if err != nil {
				return nil, fmt.Errorf("读取图片失败 %s: %w", s, err)
			}
			mime := "image/jpeg"
			if strings.HasSuffix(s, ".png") {
				mime = "image/png"
			} else if strings.HasSuffix(s, ".gif") {
				mime = "image/gif"
			} else if strings.HasSuffix(s, ".webp") {
				mime = "image/webp"
			}
			out = append(out, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(b))
		}
	}
	return out, nil
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

// workerBuilder 构造 supervisor 专业 worker 的"每请求新建" Agent 工厂
// （与 cmd/server/workers.go 同构；worker 各有专属提示词与工具子集）。
func workerBuilder(router *provider.Router, prompts *prompt.Registry, mem *memory.Manager, skillReg *skill.Registry, ragIndex *rag.Index, promptName string, reg *tool.Registry) func() *agent.Agent {
	toolHook, skillHook := traceHooks()
	return func() *agent.Agent {
		return agent.New(agent.Config{
			Router:  router,
			Tools:   reg,
			Prompts: prompts,
			Mem:     mem,
			// CONTEXT_MAX_TOKENS 上下文预算（默认 4000）；SUMMARY_MAX_CHARS 摘要长度（默认 600）
			Window:    &agent.ContextWindow{MaxTokens: atoiDefault(os.Getenv("CONTEXT_MAX_TOKENS"), 4000), Summarizer: agent.PrefixSummarizer{MaxChars: atoiDefault(os.Getenv("SUMMARY_MAX_CHARS"), 600)}},
			Moderator: safety.NewKeywordModerator(),
			// MAX_TOOL_TURNS 最大工具轮数（默认 5）
			MaxTurns:     atoiDefault(os.Getenv("MAX_TOOL_TURNS"), 5),
			PromptName:   promptName,
			Skills:       skillReg,
			RAG:          ragIndex,
			RewriteQuery: os.Getenv("ZEBRA_QUERY_REWRITE") == "1",
			OnTool:       toolHook,
			OnSkill:      skillHook,
		})
	}
}

// traceHooks 返回终端活动轨迹钩子：▲ 工具调用（含成败）、■ 技能注入。
func traceHooks() (func(name string, args map[string]interface{}, ok bool, err error), func(names []string)) {
	toolHook := func(name string, args map[string]interface{}, ok bool, err error) {
		detail, _ := json.Marshal(args)
		sym := console.Symbol("▲", console.ColorTool)
		if ok {
			fmt.Printf("  %s 工具调用: %s(%s) ✓\n", sym, name, detail)
		} else {
			fmt.Printf("  %s 工具调用: %s(%s) ✗ %v\n", sym, name, detail, err)
		}
	}
	skillHook := func(names []string) {
		fmt.Printf("  %s 技能注入: %s\n", console.Symbol("■", console.ColorSkill), strings.Join(names, ", "))
	}
	return toolHook, skillHook
}

// modeLabel 模式名 → 终端展示文案。
func modeLabel(m string) string {
	switch m {
	case "chat":
		return "chat 普通对话"
	case "plan":
		return "plan 规划-执行"
	case "react":
		return "react ReAct 推理-行动"
	case "reflect":
		return "reflect 回答后反思改进"
	case "debate":
		return "debate 双 Agent 辩论"
	case "supervisor":
		return "supervisor 多 Agent 路由"
	case "consistent":
		return "consistent 自一致性采样择优"
	}
	return m
}

// validMode 判断模式名是否受支持。
func validMode(m string) bool {
	switch m {
	case "chat", "plan", "react", "reflect", "debate", "supervisor", "consistent":
		return true
	}
	return false
}

// emitTerminal 把 Agent 流式事件打印为终端活动轨迹（与 Web UI 活动块对齐）：
// ◇ 阶段（规划/执行步骤/思考/观察等）。技能/工具由 OnSkill/OnTool 钩子
// 统一打印（含命中技能名与调用成败），这里跳过避免重复。
func emitTerminal(ev agent.Event) {
	switch ev.Type {
	case agent.EventPhase:
		fmt.Printf("  %s %s\n", console.Symbol("◇", console.ColorModel), ev.Phase)
	}
}

// runAgent 按模式分发执行：chat 走普通对话；plan/react 走流式（活动轨迹）；
// reflect/debate/supervisor 先打印阶段提示再执行。images 为该轮附带的多模态
// 图片（可空）。
func runAgent(ctx context.Context, ag *agent.Agent, sup *supervisor.Supervisor, history *[]provider.Message, mode string, reactMaxSteps int, images []string, input string) (string, error) {
	opts := agent.RunOptions{Images: images}
	switch mode {
	case "chat":
		return ag.Run(ctx, input, opts)
	case "plan":
		return ag.PlanAndExecuteStream(ctx, input, opts, emitTerminal)
	case "react":
		if reactMaxSteps <= 0 {
			reactMaxSteps = 6 // agent 层默认兜底
		}
		return ag.ReActStream(ctx, input, opts, reactMaxSteps, emitTerminal)
	case "reflect":
		emitTerminal(agent.Event{Type: agent.EventPhase, Phase: "回答后反思改进…"})
		// RunReflect 统一处理修订版的审核与历史写回
		return ag.RunReflect(ctx, input, opts)
	case "debate":
		emitTerminal(agent.Event{Type: agent.EventPhase, Phase: "双 Agent 辩论中…"})
		reply, _, err := ag.Debate(ctx, input, "", "", images...)
		return reply, err
	case "supervisor":
		if sup == nil {
			return "", fmt.Errorf("多 Agent supervisor 未装配")
		}
		emitTerminal(agent.Event{Type: agent.EventPhase, Phase: "多 Agent 路由中…"})
		// 先路由并展示结果，再执行 worker —— 与 supervisor.Run 同构，但顺序更利于学习：
		// "◇ 多 Agent 路由中… → ◇ 已路由: data → ▲ 工具调用…"
		w, rerr := sup.Route(ctx, input)
		if rerr != nil {
			return "", rerr
		}
		fmt.Printf("  %s 已路由: %s（%s）\n", console.Symbol("◇", console.ColorModel), w.Name, w.Description)
		workerAg := w.Build()
		workerAg.Bind("zebra", "admin", "local", history)
		return workerAg.Run(ctx, input, opts)
	case "consistent":
		// 自一致性：独立采样多份回答再择优，降低单次采样随机性。
		// 采样数可配（SELF_CONSISTENT_SAMPLES，默认 3）。
		samples := atoiDefault(os.Getenv("SELF_CONSISTENT_SAMPLES"), 3)
		emitTerminal(agent.Event{Type: agent.EventPhase, Phase: fmt.Sprintf("自一致性采样 %d 份回答中…", samples)})
		return ag.SelfConsistent(ctx, input, samples, images...)
	}
	return "", fmt.Errorf("未知模式: %s", mode)
}

// replCommands 返回 REPL 支持的命令列表（用于 Tab 补全提示）。
func replCommands() []string {
	return []string{
		"exit",
		"/help",
		"/mode",
		"/modes",
		"/env",
		"/model",
		"/provider",
		"/tools",
		"/mcp",
		"/skills",
		"/mem",
		"/rag",
		"/voice",
		"/image",
		"/caps",
	}
}

// printHelp 打印 REPL 支持的命令清单（模式与各能力明细通过 /modes 等命令查看）。
func printHelp() {
	fmt.Println()
	for _, l := range []string{
		"exit         退出",
		"/help        显示本命令清单",
		"/mode        查看当前模式",
		"/mode <名称> 切换对话模式",
		"/modes       查看全部模式及说明",
		"/env         查看生效的环境变量配置",
		"/model       查看当前使用的模型",
		"/provider    查看供应商路由链",
		"/tools       查看已注册工具明细",
		"/mcp         查看 MCP 连接与工具明细",
		"/skills      查看已加载技能明细",
		"/mem         查看记忆模式",
		"/rag         查看知识库状态",
		"/voice       查看语音能力",
		"/image       查看已加载的多模态图片",
		"/caps        查看完整能力清单",
		"",
		"输入 /modes 查看更多，或直接输入对话内容开始提问。",
	} {
		fmt.Println("  " + l)
	}
}

// printModes 打印全部对话模式及说明（原 printHelp 中的模式部分）。
func printModes() {
	fmt.Println("  对话模式")
	for _, m := range []string{"chat", "plan", "react", "reflect", "debate", "supervisor", "consistent"} {
		fmt.Printf("    %s\n", modeLabel(m))
	}
	fmt.Println("  用法: /mode <名称> 切换；/mode 查看当前。")
}
