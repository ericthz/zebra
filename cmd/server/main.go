// cmd/server —— 企业版入口：HTTP API 服务（A1）。
//
// 启动依赖（均可通过环境变量注入）：
//
//	OLLAMA_BASE_URL / OLLAMA_MODEL    主模型（Ollama，支持流式）
//	FALLBACK_BASE_URL / FALLBACK_MODEL 备选模型（OpenAI 兼容，故障自动降级 C15）
//	ANTHROPIC_API_KEY                  可选 Anthropic 备选
//	QDRANT_URL                         长期记忆（可选，不可用则自动降级为仅工作记忆）
//	ADMIN_KEY                          管理员 API Key（RBAC admin）
//	USER_KEY                           普通用户 API Key（RBAC user，工具受限）
//	OPENAI_API_KEY / OPENAI_BASE_URL   嵌入（OpenAI 兼容；默认用 Ollama 本地嵌入）
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/cost"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/mcp"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/server"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)) // B5 结构化日志
	slog.SetDefault(logger)

	// ---- 密钥（D19：从环境注入，生产接 KMS/Vault）----
	secrets := safety.EnvSecretStore{}
	adminKey, _ := secrets.Get("ADMIN_KEY")
	userKey, _ := secrets.Get("USER_KEY")
	if adminKey == "" {
		logger.Warn("ADMIN_KEY 未设置，使用默认 admin-key（仅限本地演示）")
		adminKey = "admin-key"
	}
	if userKey == "" {
		userKey = "user-key"
	}

	// ---- A3 API Key 注册（RBAC：admin / user 两级）----
	keys := server.NewKeyStore()
	keys.Register(server.Principal{Key: adminKey, User: "admin", Role: "admin", Tenant: "default"})
	keys.Register(server.Principal{Key: userKey, User: "alice", Role: "user", Tenant: "default"})

	// ---- Provider 路由（C15）：主 Ollama（流式）+ 备选 OpenAI 兼容 ----
	httpCli := provider.NewHTTPClient(15*time.Second, 2, 300*time.Millisecond) // B7
	var chain []provider.Provider

	chain = append(chain, &provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "llama3.1"),
		Client:  httpCli,
	})
	if fb := os.Getenv("FALLBACK_BASE_URL"); fb != "" {
		chain = append(chain, &provider.OpenAIProvider{
			BaseURL: fb, Model: envOr("FALLBACK_MODEL", "gpt-4o-mini"),
			APIKey: os.Getenv("OPENAI_API_KEY"), Client: httpCli,
		})
	}
	if ak := os.Getenv("ANTHROPIC_API_KEY"); ak != "" {
		chain = append(chain, &provider.AnthropicProvider{
			BaseURL: envOr("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
			Model:   envOr("ANTHROPIC_MODEL", "claude-3-5-haiku-latest"),
			APIKey:  ak, Client: httpCli,
		})
	}
	router := provider.NewRouter(chain...)

	// ---- 工具注册 + 权限白名单（D20）----
	reg := tool.NewRegistry()
	reg.Register(&tool.WeatherTool{})
	reg.Register(&tool.CalculatorTool{})
	reg.Register(&tool.DateTimeTool{})
	reg.Register(&tool.RandomTool{})
	reg.Register(&tool.SearchTool{})
	reg.Register(&tool.UnitConverterTool{})
	reg.Register(&tool.TranslateTool{})
	reg.Register(&tool.IPInfoTool{})

	// RBAC 收敛：普通用户仅开放安全只读工具
	for _, name := range []string{"calculator", "get_current_datetime", "generate_random_number",
		"convert_units", "translate_text"} {
		reg.AllowTool("user", name)
	}
	reg.DenyTool("user", "web_search") // 示例：收回搜索权限

	// 可选：挂载 MCP 远端工具（保持与既有能力一致）
	registerMCPTools(reg, logger)

	// ---- P2 本地执行：文件读写 + 命令执行（沙箱隔离 + 高危二次确认）----
	// 工作目录白名单：默认 ./workspace；只读模式默认开启（写文件/命令需显式放开）。
	execSandbox := tool.NewExecSandbox(envOr("EXEC_WORKDIR", "workspace"), envOr("EXEC_READONLY", "1") == "1")
	reg.Register(&tool.ListDirTool{Sandbox: execSandbox})
	reg.Register(&tool.ReadFileTool{Sandbox: execSandbox})
	reg.Register(&tool.WriteFileTool{Sandbox: execSandbox})
	reg.Register(&tool.RunCommandTool{Sandbox: execSandbox})

	// ---- P1 技能体系：扫描 skills/ 目录注册技能（技能检索与注入由 Agent 完成）----
	skillReg := skill.NewRegistry()
	if loaded, err := skill.LoadDir("skills"); err == nil && len(loaded) > 0 {
		skillReg.LoadAll(loaded)
		for _, sk := range loaded {
			logger.Info("已加载技能", "name", sk.Name, "version", sk.Version)
		}
	} else if err != nil {
		logger.Warn("技能目录加载失败（继续运行，技能检索关闭）", "err", err)
	}

	// ---- C16 系统提示模板（版本化）----
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: `你是 zebra 企业级 AI 助手。
你拥有工具调用能力，回答尽量简洁准确。当前用户角色：{role}。`})
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v2", Text: `你是 zebra 企业级 AI 助手（v2 灰度版）。
你拥有工具调用能力，回答尽量简洁准确。当前用户角色：{role}。`})
	prompts.Activate("assistant", "v1")

	// ---- C12 记忆：工作记忆 + 可选 Qdrant 长期记忆 ----
	working := memory.NewWorkingMemory(10)
	var mem *memory.Manager
	mem = memory.NewManager(working, nil) // 先只启工作记忆

	if q := os.Getenv("QDRANT_URL"); q != "" {
		embed := embedder()
		qmem := memory.NewQdrantMemory(q, "zebra_mem", 768, embed)
		// 就绪探针：Qdrant 不可用时自动降级为仅工作记忆（B7 降级）
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if _, err := qmem.Retrieve(ctx, "ping", 1); err == nil {
			mem = memory.NewManager(working, qmem)
			logger.Info("长期记忆已启用", "qdrant", q)
		} else {
			logger.Warn("Qdrant 不可用，降级为仅工作记忆", "err", err)
		}
		cancel()
	}

	// ---- 指标（B5/P3）----
	metrics := server.NewMetrics() // 工具成功率指标记录 + /metrics 暴露

	// ---- P5 成本治理：成本归因追踪器（挂 /metrics/cost）----
	costTracker := cost.NewTracker()

	// ---- P5 语义缓存：复用嵌入器做语义相似度命中（相似问题直接回答案省钱）----
	var semanticCache *cache.SemanticCache
	if emb := embedder(); emb != nil {
		semanticCache = cache.New(emb, 0.92, 200)
	}

	// ---- P4 主动出站：Webhook 通知器（可选，WEBHOOK_URL 为空则关闭）----
	var notifier notify.Notifier
	if wh := os.Getenv("WEBHOOK_URL"); wh != "" {
		notifier = notify.NewWebhookNotifier(wh, os.Getenv("WEBHOOK_SECRET"))
		logger.Info("已启用 Webhook 通知", "url", wh)
	}

	// ---- 安全横切（D17/D18/D20）----
	moderator := safety.NewKeywordModerator() // 空敏感词表 = 演示用
	audit := safety.NewStdAuditLog(logger)
	reg.SetAuditor(server.NewToolAuditor(audit, metrics)) // D20 审计 + P3 工具成功率指标

	// ---- 会话 / 限流 ----
	sessions := server.NewInMemoryStore(30 * time.Minute) // A2
	rate := server.NewRateLimiter(2, 5)                   // B6：每用户每秒 2 次、突发 5 次

	api := server.NewAPIServer(server.Deps{
		Router:     router,
		Tools:      reg,
		Prompts:    prompts,
		Mem:        mem,
		Window:     &agent.ContextWindow{MaxTokens: 4000, Summarizer: agent.PrefixSummarizer{MaxChars: 600}}, // C11
		Moderator:  moderator,
		Audit:      audit,
		Sessions:   sessions,
		Keys:       keys,
		Rate:       rate,
		Logger:     logger,
		Metrics:    metrics,
		MaxTurns:   5,
		PromptName: "assistant",
		Skills:     skillReg,
		Notifier:   notifier,
		Cost:       costTracker,
		Cache:      semanticCache,
		Model:      envOr("OLLAMA_MODEL", "llama3.1"),
	})

	addr := envOr("ADDR", ":8080")
	if err := api.Serve(context.Background(), addr); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func embedder() memory.Embedder {
	base := envOr("OLLAMA_BASE_URL", "http://localhost:11434")
	model := envOr("EMBED_MODEL", "nomic-embed-text:v1.5")
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		return &memory.OpenAIEmbedder{
			BaseURL: envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			Model:   envOr("OPENAI_EMBED_MODEL", "text-embedding-3-small"),
			APIKey:  apiKey, Client: &http.Client{Timeout: 10 * time.Second},
		}
	}
	return &memory.OllamaEmbedder{BaseURL: base, Model: model, Client: &http.Client{Timeout: 10 * time.Second}}
}

func registerMCPTools(reg *tool.Registry, logger *slog.Logger) {
	mode := strings.ToLower(os.Getenv("MCP_MODE"))
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var client *mcp.Client
	switch mode {
	case "stdio":
		cmd := os.Getenv("MCP_COMMAND")
		if cmd == "" {
			return
		}
		parts := strings.Fields(cmd)
		tr, err := mcp.NewStdioClient(parts[0], parts[1:]...)
		if err != nil {
			logger.Warn("MCP stdio 启动失败", "err", err)
			return
		}
		client = mcp.NewClient(tr)
	case "http":
		tr := mcp.NewHTTPClient(envOr("MCP_HTTP_URL", "http://localhost:9000"))
		client = mcp.NewClient(tr)
	}
	if client == nil {
		return
	}
	if err := client.Initialize(ctx); err != nil { // 握手（官方规范）
		logger.Warn("MCP initialize 失败", "err", err)
		return
	}
	defs, err := client.ListTools(ctx)
	if err != nil {
		logger.Warn("MCP tools/list 失败", "err", err)
		return
	}
	for _, def := range defs {
		reg.Register(mcp.NewMCPToolAdapter(client, def))
		logger.Info("已注册 MCP 工具", "name", def.Name)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
