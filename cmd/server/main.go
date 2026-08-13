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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/cost"
	"github.com/ericthz/zebra/internal/eval"
	"github.com/ericthz/zebra/internal/feedback"
	"github.com/ericthz/zebra/internal/mcp"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/server"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/supervisor"
	"github.com/ericthz/zebra/internal/task"
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
	// HTTP_TIMEOUT 可调（本地大模型首 token 慢，默认 60s；生产按 SLO 收紧）
	httpCli := provider.NewHTTPClient(time.Duration(atoiDefault(os.Getenv("HTTP_TIMEOUT"), 60))*time.Second, 1, 300*time.Millisecond) // B7
	var chain []provider.Provider

	chain = append(chain, &provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
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

	reg.Register(&tool.FetchURLTool{}) // P6 SSRF 防护的抓取工具
	// ---- P2 本地执行：文件读写 + 命令执行（沙箱隔离 + 高危二次确认）----
	// 工作目录白名单：默认 ./workspace；只读模式默认开启（写文件/命令需显式放开）。
	execSandbox := tool.NewExecSandbox(envOr("EXEC_WORKDIR", "workspace"), envOr("EXEC_READONLY", "1") == "1")
	reg.Register(&tool.ListDirTool{Sandbox: execSandbox})
	reg.Register(&tool.ReadFileTool{Sandbox: execSandbox})
	reg.Register(&tool.WriteFileTool{Sandbox: execSandbox})
	reg.Register(&tool.RunCommandTool{Sandbox: execSandbox})
	// ---- P23 文档/图表产出：Word/PDF/SVG 图表（沙箱内落盘，admin-only）----
	reg.Register(&tool.GenerateDocxTool{Sandbox: execSandbox})
	reg.Register(&tool.GenerateChartTool{Sandbox: execSandbox})

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
	// P13 多 Agent：专业 worker 专属提示词（persona）
	prompts.Register(&prompt.Template{Name: "data", Version: "v1", Text: `你是 zebra 的【数据专家 Agent】。
你擅长数学计算、单位换算、文本翻译、日期时间等数据处理任务。回答给出精确数值与计算过程。角色：{role}。`})
	prompts.Register(&prompt.Template{Name: "knowledge", Version: "v1", Text: `你是 zebra 的【知识专家 Agent】。
你擅长搜索资料、抓取网页、查阅本地文档。回答必须基于检索/抓取到的信息并注明来源，不要编造。角色：{role}。`})
	prompts.Activate("assistant", "v1")

	// P18 提示词模板文件化：若 prompts/ 目录存在则加载（改文件即热更新，无需改代码）
	if fileTemplates, err := prompt.LoadDir("prompts"); err == nil && len(fileTemplates) > 0 {
		prompts.LoadAll(fileTemplates)
		logger.Info("已从 prompts/ 加载模板", "count", len(fileTemplates))
	}

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
	var ragIndex *rag.Index
	if emb := embedder(); emb != nil {
		semanticCache = cache.New(emb, 0.92, 200)

		// ---- P8 RAG 知识库：加载 docs/ 目录文档（可选）----
		ragIndex = rag.NewIndex(emb)
		if docs, err := loadDocs("docs"); err == nil && len(docs) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			for name, content := range docs {
				if derr := ragIndex.AddDocument(ctx, content, name, 600, 100); derr != nil {
					logger.Warn("RAG 文档摄入失败", "doc", name, "err", derr)
					continue
				}
				logger.Info("已摄入知识文档", "doc", name)
			}
			cancel()
		} else {
			logger.Warn("docs/ 目录无文档，RAG 知识库为空（仍可用，检索无命中）")
		}
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

	// ---- 会话 / 限流 / 异步任务 ----
	sessions := server.NewInMemoryStore(30 * time.Minute) // A2
	rate := server.NewRateLimiter(2, 5)                   // B6：每用户每秒 2 次、突发 5 次
	taskStore := task.NewInMemoryStore()                  // P12 异步任务存储（生产换 Redis/DB）
	fbStore := feedback.NewInMemoryStore()                // P16 反馈闭环存储

	// ---- P22 用户画像：对话自动学习 + 遗忘策略（TTL 保鲜 + 容量治理）----
	profileStore := memory.NewProfileStore()
	// P27 抽取器升级：默认 LLM 语义抽取 + 规则回退（PROFILE_LLM=0 可退回纯规则）
	var profileExtractor memory.Extractor = memory.RuleExtractor{}
	if os.Getenv("PROFILE_LLM") != "0" && router != nil {
		profileExtractor = &memory.LLMExtractor{Router: router, Timeout: 15 * time.Second}
	}
	profilePolicy := &memory.ForgetPolicy{
		TTL:             time.Duration(atoiDefault(os.Getenv("PROFILE_TTL_HOURS"), 24*30)) * time.Hour, // 默认 30 天保鲜
		MaxFactsPerUser: 50,
		OnForget: func(user, key string) {
			logger.Info("画像事实被遗忘（容量裁剪）", "user", user, "key", key)
		},
	}
	// 启动时跑一次全量遗忘清理（生产可接定时任务）
	if n := profilePolicy.Apply(profileStore, time.Now()); n > 0 {
		logger.Info("画像遗忘清理完成", "forgotten", n)
	}

	// ---- P25 语音交互：OpenAI 兼容 ASR/TTS（可选，VOICE_BASE_URL 开启）----
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
		logger.Info("已启用语音交互", "base", vb, "asr", voice.ASRModel, "tts", voice.TTSModel)
	}

	// ---- P13 多 Agent Supervisor：数据/知识/常规 三个专业 worker ----
	var supervisorInst *supervisor.Supervisor
	if router != nil && prompts != nil {
		supervisorInst = buildSupervisor(workerDeps{
			router: router, prompts: prompts, mem: mem, window: &agent.ContextWindow{MaxTokens: 4000, Summarizer: agent.PrefixSummarizer{MaxChars: 600}},
			moderator: moderator, skills: skillReg, cache: semanticCache, rag: ragIndex,
			model: envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"), maxTurns: 5, cost: costTracker,
		}, reg)
	}

	// ---- P18 配置热更新：重载 技能/提示词/知识库（不重启）----
	var reload func() error
	if skillReg != nil && prompts != nil {
		reload = func() error {
			// 1. 技能
			if loaded, err := skill.LoadDir("skills"); err != nil {
				return err
			} else if len(loaded) > 0 {
				skillReg.LoadAll(loaded)
				logger.Info("热重载技能", "count", len(loaded))
			}
			// 2. 提示词
			if loaded, err := prompt.LoadDir("prompts"); err != nil {
				return err
			} else if len(loaded) > 0 {
				prompts.LoadAll(loaded)
				logger.Info("热重载提示词", "count", len(loaded))
			}
			// 3. RAG 知识库（清空重建）
			if ragIndex != nil {
				ragIndex.Reset()
				if docs, err := loadDocs("docs"); err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					for name, content := range docs {
						if derr := ragIndex.AddDocument(ctx, content, name, 600, 100); derr != nil {
							return derr
						}
					}
					logger.Info("热重载知识库", "docs", len(docs))
				}
			}
			return nil
		}
	}

	// ---- P21 在线评测/影子模式：候选模型 + 评审器（可选）----
	// ZEBRA_SHADOW_MODEL 开启；候选默认走 Ollama 同后端，ZEBRA_SHADOW_OPENAI=1
	// 则走 OpenAI 兼容（可用 FALLBACK 网关/新模型做对比）。
	// ZEBRA_SHADOW_SAMPLE 为自动采样率百分比（0~100），0 表示仅显式触发。
	var shadowEval *eval.ShadowEvaluator
	if shadowModel := os.Getenv("ZEBRA_SHADOW_MODEL"); shadowModel != "" {
		var candidate provider.Provider
		if os.Getenv("ZEBRA_SHADOW_OPENAI") == "1" {
			candidate = &provider.OpenAIProvider{
				BaseURL: envOr("ZEBRA_SHADOW_BASE_URL", envOr("FALLBACK_BASE_URL", "https://api.openai.com/v1")),
				Model:   shadowModel,
				APIKey:  os.Getenv("OPENAI_API_KEY"),
				Client:  httpCli,
			}
		} else {
			candidate = &provider.OllamaProvider{
				BaseURL: envOr("ZEBRA_SHADOW_BASE_URL", envOr("OLLAMA_BASE_URL", "http://localhost:11434")),
				Model:   shadowModel,
				Client:  httpCli,
			}
		}
		sample := float64(atoiDefault(os.Getenv("ZEBRA_SHADOW_SAMPLE"), 10)) / 100
		shadowEval = eval.NewShadowEvaluator(candidate, eval.NewJudge(router), eval.NewShadowStore(200), sample)
		shadowEval.Metrics = metrics.Inc
		shadowEval.Log = logger
		logger.Info("已启用影子评测", "candidate", shadowModel, "sample_rate", sample)
	}

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
		RAG:        ragIndex,
		Model:      envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
		TaskStore:  taskStore,
		Supervisor: supervisorInst,
		Feedback:   fbStore,
		Reload:     reload,
		Shadow:     shadowEval,
		Profile:    profileStore,
		ProfileTTL: profilePolicy.TTL,
		Extractor:  profileExtractor,
		Voice:      voice,
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

// loadDocs 读取 docs/ 目录下的 .md / .txt 文档（RAG 摄取源）。
func loadDocs(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		out[name] = string(data)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no docs found")
	}
	return out, nil
}
