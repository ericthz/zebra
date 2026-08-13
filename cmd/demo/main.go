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
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
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
		Model:   envOr("OLLAMA_MODEL", "llama3.1"),
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

	// ---- P1 技能体系：加载 skills/ 目录 ----
	skillReg := skill.NewRegistry()
	if loaded, err := skill.LoadDir("skills"); err == nil {
		skillReg.LoadAll(loaded)
	}

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
	})

	history := make([]provider.Message, 0)
	ag.Bind("demo", "admin", "local", &history)

	fmt.Println("🤖 zebra CLI Agent（输入 exit 退出）")
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
