// cmd/demo —— 单机 CLI 版 Agent（学习入口，企业能力走 cmd/server）。
//
// 用法：
//
//	go run ./cmd/demo
//	go run ./cmd/server          # 企业版 HTTP 服务
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/console"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	// ---- LLM（默认 Ollama 原生，流式）----
	httpCli := provider.NewHTTPClient(20*time.Second, 2, 300*time.Millisecond)
	router := provider.NewRouter(&provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
		Client:  httpCli,
	})

	// ---- 工具（demo 全量开放）----
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
	// ---- P2 本地执行：文件读写 + 命令执行（demo 默认可写，便于演示 Agentic 能力）----
	execSandbox := tool.NewExecSandbox("workspace", false)
	reg.Register(&tool.ListDirTool{Sandbox: execSandbox})
	reg.Register(&tool.ReadFileTool{Sandbox: execSandbox})
	reg.Register(&tool.WriteFileTool{Sandbox: execSandbox})
	reg.Register(&tool.RunCommandTool{Sandbox: execSandbox})

	// ---- P1 技能体系：加载 skills/ 目录 ----
	skillReg := skill.NewRegistry()
	if loaded, err := skill.LoadDir("skills"); err == nil {
		skillReg.LoadAll(loaded)
	}
	skills := skillReg.List()

	// ---- 记忆：仅工作记忆（单机演示不依赖 Qdrant）----
	mem := memory.NewManager(memory.NewWorkingMemory(10), nil)

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
		// P31 执行痕迹：工具调用与技能注入在终端可见，学习 Agent 行为
		OnTool: func(name string, args map[string]interface{}, ok bool, err error) {
			detail, _ := json.Marshal(args)
			if ok {
				fmt.Printf("  🛠 工具调用: %s(%s) ✓\n", name, detail)
			} else {
				fmt.Printf("  🛠 工具调用: %s(%s) ❌ %v\n", name, detail, err)
			}
		},
		OnSkill: func(names []string) {
			fmt.Printf("  📚 技能注入: %s\n", strings.Join(names, ", "))
		},
	})

	history := make([]provider.Message, 0)
	ag.Bind("demo", "admin", "local", &history)

	// ---- P31 启动清单：一眼看清这台 Agent 有什么 ----
	fmt.Println("🤖 zebra CLI Agent（输入 exit 退出）")
	label := func(s string) string { return console.Pad(s, 12) } // 标签列定宽，冒号对齐
	fmt.Printf("  %s: %s\n", label("🤖 模型"), router.Primary().Name())
	toolNames := reg.Names()
	fmt.Printf("  %s: %d 个\n", label("🛠 工具"), len(toolNames))
	for _, row := range console.Columns(toolNames, 4) {
		fmt.Printf("     %s\n", row)
	}
	if len(skills) == 0 {
		fmt.Printf("  %s: 无\n", label("📚 技能"))
	} else {
		fmt.Printf("  %s: %d 个\n", label("📚 技能"), len(skills))
		maxName := 0
		for _, sk := range skills {
			if n := len(sk.Name); n > maxName {
				maxName = n
			}
		}
		for _, sk := range skills {
			fmt.Printf("     %s  %s\n", console.Pad(sk.Name, maxName+2), sk.Description)
		}
	}
	fmt.Printf("  %s: demo 未启用（企业版 cmd/server 支持）\n", label("🔌 MCP"))
	fmt.Printf("  %s: 工作记忆（单机）\n", label("🧠 记忆"))
	fmt.Println(strings.Repeat("─", 60))
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 ")
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
			fmt.Printf("❌ %v\n", err)
			continue
		}
		fmt.Println("🤖", answer)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
