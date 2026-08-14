// cmd/zebra —— 本地命令行 Agent 客户端（交互式终端；企业能力走 cmd/server）。
//
// 用法：
//
//	go run ./cmd/zebra
//	go run ./cmd/server          # 企业版 HTTP 服务
package main

import (
	"context"
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
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/supervisor"
	"github.com/ericthz/zebra/internal/tool"
)

func main() {
	// ---- 启动模式：与 Web UI 的模式选择一致 ----
	mode := flag.String("mode", "chat", "启动模式: chat|plan|react|reflect|debate|supervisor")
	flag.Parse()
	// ReAct 最大推理-行动步数（默认 6；REACT_MAX_STEPS 可调）
	reactMaxSteps := atoiDefault(os.Getenv("REACT_MAX_STEPS"), 6)

	// ---- P37 终端 banner ----
	observe.PrintBanner(os.Stdout, "Zebra CLI — 本地命令行 Agent 客户端（输入 exit 退出）")

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

	reg.Register(&tool.FetchURLTool{}) // P6 SSRF 防护的抓取工具
	// ---- P2 本地执行：文件读写 + 命令执行（Zebra CLI 默认可写，便于演示 Agentic 能力）----
	execSandbox := tool.NewExecSandbox("workspace", false)
	reg.Register(&tool.ListDirTool{Sandbox: execSandbox})
	reg.Register(&tool.ReadFileTool{Sandbox: execSandbox})
	reg.Register(&tool.WriteFileTool{Sandbox: execSandbox})
	reg.Register(&tool.RunCommandTool{Sandbox: execSandbox})
	// ---- P32 MCP 远端工具：与 cmd/server 同一装配（MCP_MODE 设置即启用）----
	mcpMode, mcpDefs := mcp.RegisterTools(reg, logger)
	// ---- P53 插件动态加载：plugins/ 目录 JSON 定义的外部 HTTP 工具 ----
	if _, err := os.Stat("plugins"); err == nil {
		if defs, lerr := plugin.Load("plugins"); lerr == nil && len(defs) > 0 {
			plugin.Register(reg, defs, &http.Client{Timeout: 10 * time.Second})
		}
	}

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
	prompts.Register(&prompt.Template{Name: "data", Version: "v1", Text: `你是 zebra 的【数据专家 Agent】。擅长计算、单位换算、翻译、日期时间等数据处理任务。调用合适的工具得出准确结果，回答简洁。角色：{role}。`})
	prompts.Register(&prompt.Template{Name: "knowledge", Version: "v1", Text: `你是 zebra 的【知识专家 Agent】。擅长搜索资料、抓取网页、查阅文档等知识获取任务。调用合适的工具，基于事实回答并注明来源。角色：{role}。`})

	// P31 执行痕迹：工具调用与技能注入在终端可见，学习 Agent 行为（worker 复用同一钩子）
	toolHook, skillHook := traceHooks()

	ag := agent.New(agent.Config{
		Router:       router,
		Tools:        reg,
		Prompts:      prompts,
		Mem:          mem,
		Window:       &agent.ContextWindow{MaxTokens: 4000, Summarizer: agent.PrefixSummarizer{MaxChars: 600}},
		Moderator:    safety.NewKeywordModerator(),
		MaxTurns:     5,
		PromptName:   "assistant",
		Skills:       skillReg,
		RAG:          ragIndex,
		RewriteQuery: os.Getenv("ZEBRA_QUERY_REWRITE") == "1", // P48 查询改写
		OnTool:       toolHook,
		OnSkill:      skillHook,
	})

	history := make([]provider.Message, 0)
	ag.Bind("zebra", "admin", "local", &history)

	// ---- P13 多 Agent Supervisor：数据/知识/常规 三个专业 worker（与 cmd/server 同构）----
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
		Title:        "Zebra CLI Agent",
		Models:       []string{router.Primary().Name()},
		Tools:        reg,
		Skills:       skills,
		MCPMode:      mcpMode,
		MCPCount:     len(mcpDefs),
		MCPTools:     observe.FromMCP(mcpDefs),
		MemMode:      memMode,
		RAGDocs:      docsCount,
		RAGChunks:    ragChunks,
		VoiceEnabled: voice != nil,
	})
	// 模式行：2 空格缩进（清单树前缀占 4 格），标签补宽 2 格使冒号与清单各列对齐
	modeLbl := console.Pad(console.Symbol("◇", console.ColorModel)+" 模式", observe.LabelWidth+2)
	fmt.Printf("  %s: %s（输入 /mode 切换，/help 查看全部）\n", modeLbl, modeLabel(*mode))
	fmt.Println(strings.Repeat("─", 60))
	for {
		// P38：raw 模式 + UTF-8 感知行编辑（中文退格不再残留字节残片）；
		// 非 TTY 自动回退标准行读取。
		in, err := console.ReadLine(console.Symbol(">", console.ColorTitle) + " ")
		if err != nil {
			break // EOF / Ctrl-C / 中断
		}
		in = strings.TrimSpace(in)
		if in == "" {
			continue
		}
		if in == "exit" {
			break
		}
		// ---- REPL 命令：模式选择 / 帮助 ----
		switch {
		case in == "/help" || in == "help" || in == "?":
			printHelp()
			continue
		case in == "/mode":
			fmt.Printf("  当前模式: %s\n", modeLabel(*mode))
			continue
		case strings.HasPrefix(in, "/mode "):
			name := strings.TrimSpace(strings.TrimPrefix(in, "/mode "))
			if !validMode(name) {
				fmt.Printf("  未知模式: %s（可用: chat|plan|react|reflect|debate|supervisor）\n", name)
				continue
			}
			*mode = name
			fmt.Printf("  已切换模式: %s\n", modeLabel(*mode))
			continue
		}
		ctx := context.Background()
		answer, err := runAgent(ctx, ag, sup, &history, *mode, reactMaxSteps, in)
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

// workerBuilder 构造 supervisor 专业 worker 的"每请求新建" Agent 工厂
// （与 cmd/server/workers.go 同构；worker 各有专属提示词与工具子集）。
func workerBuilder(router *provider.Router, prompts *prompt.Registry, mem *memory.Manager, skillReg *skill.Registry, ragIndex *rag.Index, promptName string, reg *tool.Registry) func() *agent.Agent {
	toolHook, skillHook := traceHooks()
	return func() *agent.Agent {
		return agent.New(agent.Config{
			Router:       router,
			Tools:        reg,
			Prompts:      prompts,
			Mem:          mem,
			Window:       &agent.ContextWindow{MaxTokens: 4000, Summarizer: agent.PrefixSummarizer{MaxChars: 600}},
			Moderator:    safety.NewKeywordModerator(),
			MaxTurns:     5,
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
		return "Chat 普通对话"
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
	}
	return m
}

// validMode 判断模式名是否受支持。
func validMode(m string) bool {
	switch m {
	case "chat", "plan", "react", "reflect", "debate", "supervisor":
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
// reflect/debate/supervisor 先打印阶段提示再执行。
func runAgent(ctx context.Context, ag *agent.Agent, sup *supervisor.Supervisor, history *[]provider.Message, mode string, reactMaxSteps int, input string) (string, error) {
	opts := agent.RunOptions{}
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
		answer, err := ag.Run(ctx, input, opts)
		if err != nil {
			return "", err
		}
		return ag.Reflect(ctx, input, answer)
	case "debate":
		emitTerminal(agent.Event{Type: agent.EventPhase, Phase: "双 Agent 辩论中…"})
		reply, _, err := ag.Debate(ctx, input, "", "")
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
	}
	return "", fmt.Errorf("未知模式: %s", mode)
}

// printHelp 打印 REPL 命令与模式说明。
func printHelp() {
	fmt.Println("  Zebra CLI 帮助")
	fmt.Println("  命令:")
	fmt.Println("    exit          退出")
	fmt.Println("    /mode         查看当前模式")
	fmt.Println("    /mode <名称>  切换对话模式")
	fmt.Println("    /help         显示本帮助")
	fmt.Println("  模式:")
	for _, m := range []string{"chat", "plan", "react", "reflect", "debate", "supervisor"} {
		fmt.Printf("    %s\n", modeLabel(m))
	}
}
