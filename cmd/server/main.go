// cmd/server —— 服务端入口：HTTP API 服务。
//
// 启动依赖（均可通过环境变量注入）：
//
//	OLLAMA_BASE_URL / OLLAMA_MODEL    主模型（Ollama，支持流式）
//	FALLBACK_BASE_URL / FALLBACK_MODEL 备选模型（OpenAI 兼容，故障自动降级）
//	ANTHROPIC_API_KEY                  可选 Anthropic 备选
//	QDRANT_URL                         长期记忆（可选，不可用则自动降级为仅工作记忆）
//	ADMIN_KEY                          管理员 API Key（RBAC admin）
//	USER_KEY                           普通用户 API Key（RBAC user，工具受限）
//	OPENAI_API_KEY / OPENAI_BASE_URL   嵌入（OpenAI 兼容；默认用 Ollama 本地嵌入）
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/config"
	"github.com/ericthz/zebra/internal/cost"
	"github.com/ericthz/zebra/internal/eval"
	"github.com/ericthz/zebra/internal/feedback"
	"github.com/ericthz/zebra/internal/kg"
	"github.com/ericthz/zebra/internal/mcp"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/observe"
	"github.com/ericthz/zebra/internal/plugin"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/redis"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/schedule"
	"github.com/ericthz/zebra/internal/server"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/supervisor"
	"github.com/ericthz/zebra/internal/task"
	"github.com/ericthz/zebra/internal/tool"
)

// 默认 API Key（仅限本地演示）：绑定非回环地址时若仍使用这些值，
// 等同未配置——拒绝启动（防止已知凭据暴露到内网/公网）。
const (
	defaultAdminKey = "admin-key"
	defaultUserKey  = "user-key"
)

func main() {
	// ---- 终端 banner ----
	observe.PrintBanner(os.Stdout, "Zebra server — 服务端 AI Agent API（纯 Go 标准库）")

	// ---- 配置加载：启动时自动读取根目录 .env（零依赖）----
	// 语义：真实环境变量优先，.env 只填充"尚未设置"的变量（本地默认值）。
	// 文件不存在不算错误；解析失败仅告警，不阻断启动（避免坏 .env 拖垮服务）。
	envN, envErr := config.LoadDefault()

	// ---- 日志双写：JSON 日志同时输出到 stdout 与本地文件 ----
	// LOG_FILE 指定路径（默认 server.log），LOG_FILE=off 仅输出 stdout。
	// 文件打开失败时回退为仅 stdout，不影响服务启动（降级）。
	logWriter := io.Writer(os.Stdout)
	if fw, closeLog, ferr := config.OpenLogFile(envOr("LOG_FILE", "server.log")); ferr != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件失败，日志仅输出到 stdout: %v\n", ferr)
	} else if fw != nil {
		logWriter = io.MultiWriter(os.Stdout, fw)
		defer closeLog()
	}
	logger := slog.New(slog.NewJSONHandler(logWriter, nil)) // 结构化日志
	slog.SetDefault(logger)

	if envErr != nil {
		logger.Warn("加载 .env 失败，继续使用系统环境变量/默认值", "err", envErr)
	} else if envN > 0 {
		logger.Info("已从 .env 加载配置", "count", envN)
	}

	// ---- 密钥（从环境注入，生产接 KMS/Vault）----
	secrets := safety.EnvSecretStore{}
	adminKey, _ := secrets.Get("ADMIN_KEY")
	userKey, _ := secrets.Get("USER_KEY")

	// 默认密钥仅限本地演示：当服务绑定非 127.0.0.1 地址（对外可访问）时，
	// 未配置密钥——或配置的仍是仓库自带的默认值——直接拒绝启动（防止默认
	// admin-key/user-key 暴露到内网/公网）。绑定本机回环时才允许默认密钥，
	// 且给出显式告警。
	addr := envOr("ADDR", ":8080")
	if adminKey == "" || userKey == "" || adminKey == defaultAdminKey || userKey == defaultUserKey {
		if !bindLocalOnly(addr) {
			logger.Error("ADMIN_KEY/USER_KEY 未设置或仍为默认值，且服务绑定非本机地址，拒绝启动",
				"addr", addr, "hint", "请设置非默认的 ADMIN_KEY/USER_KEY 环境变量，或仅绑定 127.0.0.1")
			os.Exit(1)
		}
		if adminKey == "" || adminKey == defaultAdminKey {
			logger.Warn("ADMIN_KEY 未设置或为默认值，使用默认 admin-key（仅限本地演示）")
			adminKey = defaultAdminKey
		}
		if userKey == "" || userKey == defaultUserKey {
			logger.Warn("USER_KEY 未设置或为默认值，使用默认 user-key（仅限本地演示）")
			userKey = defaultUserKey
		}
	}

	// ---- API Key 注册（RBAC：admin / user 两级）----
	keys := server.NewKeyStore()
	keys.Register(server.Principal{Key: adminKey, User: "admin", Role: "admin", Tenant: "default"})
	keys.Register(server.Principal{Key: userKey, User: "alice", Role: "user", Tenant: "default"})

	// ---- Provider 路由：主 Ollama（流式）+ 备选 OpenAI 兼容 ----
	// HTTP_TIMEOUT / HTTP_RETRIES / HTTP_BACKOFF_MS / CIRCUIT_THRESHOLD /
	// CIRCUIT_COOLDOWN_SEC 可调（本地大模型首 token 慢，默认 60s；生产按 SLO 收紧）
	httpCli := provider.NewHTTPClientWithBreaker(
		time.Duration(atoiDefault(os.Getenv("HTTP_TIMEOUT"), 60))*time.Second,
		atoiDefault(os.Getenv("HTTP_RETRIES"), 1),
		time.Duration(atoiDefault(os.Getenv("HTTP_BACKOFF_MS"), 300))*time.Millisecond,
		atoiDefault(os.Getenv("CIRCUIT_THRESHOLD"), 5),
		time.Duration(atoiDefault(os.Getenv("CIRCUIT_COOLDOWN_SEC"), 30))*time.Second,
	)
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

	// ---- 工具注册 + 权限白名单----
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
	mcpMode, mcpDefs := mcp.RegisterTools(reg, logger)
	// ---- 插件动态加载：plugins/ 目录 JSON 定义的外部 HTTP 工具 ----
	var pluginNames []string
	if _, err := os.Stat("plugins"); err == nil {
		if defs, lerr := plugin.Load("plugins"); lerr == nil && len(defs) > 0 {
			pluginNames = plugin.Register(reg, defs)
			logger.Info("已注册插件工具", "count", len(pluginNames))
		}
	}

	// ---- 内容审核：提前创建，供 fetch_url 等工具抓取结果做审核（零配置即有防线）----
	moderator := safety.NewKeywordModerator()
	logger.Info("已启用内置敏感词审核", "默认词库", len(safety.DefaultBannedWords()))

	reg.Register(&tool.FetchURLTool{Moderator: moderator, BlockFetch: false}) // SSRF 防护的抓取工具
	// ---- 本地执行：文件读写 + 命令执行（沙箱隔离 + 高危二次确认）----
	// 工作目录白名单：默认 ./workspace；只读模式默认开启（写文件/命令需显式放开）。
	execSandbox := tool.NewExecSandbox(envOr("EXEC_WORKDIR", "workspace"), envOr("EXEC_READONLY", "1") == "1")
	reg.Register(&tool.ListDirTool{Sandbox: execSandbox})
	reg.Register(&tool.ReadFileTool{Sandbox: execSandbox})
	reg.Register(&tool.WriteFileTool{Sandbox: execSandbox})
	reg.Register(&tool.RunCommandTool{Sandbox: execSandbox})
	// ---- 文档/图表产出：Word/PDF/SVG 图表（沙箱内落盘，admin-only）----
	reg.Register(&tool.GenerateDocxTool{Sandbox: execSandbox})
	reg.Register(&tool.GeneratePDFTool{Sandbox: execSandbox})
	reg.Register(&tool.GenerateChartTool{Sandbox: execSandbox})

	// ---- 技能体系：扫描 skills/ 目录注册技能（技能检索与注入由 Agent 完成）----
	skillReg := skill.NewRegistry()
	if loaded, err := skill.LoadDir("skills"); err == nil && len(loaded) > 0 {
		skillReg.LoadAll(loaded)
		for _, sk := range loaded {
			logger.Info("已加载技能", "name", sk.Name, "version", sk.Version)
		}
	} else if err != nil {
		logger.Warn("技能目录加载失败（继续运行，技能检索关闭）", "err", err)
	}

	// ---- 系统提示模板（版本化）----
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: `你是 zebra AI 助手。
你拥有工具调用能力，回答尽量简洁准确。当前用户角色：{role}。`})
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v2", Text: `你是 zebra AI 助手（v2 灰度版）。
你拥有工具调用能力，回答尽量简洁准确。当前用户角色：{role}。`})
	// 多 Agent：专业 worker 专属提示词（persona）
	prompts.Register(&prompt.Template{Name: "data", Version: "v1", Text: `你是 zebra 的【数据专家 Agent】。
你擅长数学计算、单位换算、文本翻译、日期时间等数据处理任务。回答给出精确数值与计算过程。角色：{role}。`})
	prompts.Register(&prompt.Template{Name: "knowledge", Version: "v1", Text: `你是 zebra 的【知识专家 Agent】。
你擅长搜索资料、抓取网页、查阅本地文档。回答必须基于检索/抓取到的信息并注明来源，不要编造。角色：{role}。`})
	prompts.Activate("assistant", "v1")

	// 提示词模板文件化：若 prompts/ 目录存在则加载（改文件即热更新，无需改代码）
	if fileTemplates, err := prompt.LoadDir("prompts"); err == nil && len(fileTemplates) > 0 {
		prompts.LoadAll(fileTemplates)
		logger.Info("已从 prompts/ 加载模板", "count", len(fileTemplates))
	}

	// ---- 记忆：工作记忆 + 可选 Qdrant 长期记忆（与 cmd/zebra 共用装配）----
	mem, longMem := memory.SetupManager(logger)

	// ---- 指标----
	metrics := server.NewMetrics() // 工具成功率指标记录 + /metrics 暴露

	// ---- 成本治理：成本归因追踪器（挂 /metrics/cost）----
	costTracker := cost.NewTracker()

	// ---- 语义缓存：复用嵌入器做语义相似度命中（相似问题直接回答案省钱）----
	var semanticCache *cache.SemanticCache
	var ragIndex *rag.Index
	docsCount := 0
	if emb := memory.NewEmbedderFromEnv(); emb != nil {
		// CACHE_THRESHOLD 语义相似度阈值（默认 0.92，越大越严格）
		semanticCache = cache.New(emb, cacheThreshold(), atoiDefault(os.Getenv("CACHE_MAX_ENTRIES"), 200))
		// 语义缓存命中率暴露到 /metrics（zebra_cache_hits / zebra_cache_misses）
		metrics.CacheStats = semanticCache.Stats

		// ---- RAG 知识库：加载 docs/ 目录文档（可选）----
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
				logger.Info("已摄入知识文档", "doc", name)
			}
			cancel()
		} else {
			logger.Warn("docs/ 目录无文档，RAG 知识库为空（仍可用，检索无命中）")
		}
	}
	// ---- RAG 二次精排：LLM 对混合检索候选逐条打分重排（ZEBRA_RAG_RERANK=1 开启）----
	var reranker rag.Reranker
	if ragIndex != nil && router != nil && os.Getenv("ZEBRA_RAG_RERANK") == "1" {
		reranker = &rag.LLMReranker{Router: router, Timeout: 15 * time.Second}
		logger.Info("已启用 RAG LLM 精排（混合检索 → 精排截断）")
	}

	// ---- 知识图谱：从 docs/ 规则抽取实体关系（独立于嵌入器）----
	kgGraph := kg.NewGraph()
	if docs, err := rag.LoadDocs("docs"); err == nil {
		for _, content := range docs {
			for _, tr := range kg.ExtractTriples(content) {
				kgGraph.Add(tr)
			}
		}
		logger.Info("知识图谱已构建", "triples", kgGraph.Size())
	}

	// ---- 主动出站：Webhook 通知器（可选，WEBHOOK_URL 为空则关闭）----
	var notifier notify.Notifier
	if wh := os.Getenv("WEBHOOK_URL"); wh != "" {
		notifier = notify.NewWebhookNotifier(wh, os.Getenv("WEBHOOK_SECRET"))
		logger.Info("已启用 Webhook 通知", "url", redactURL(wh))
	}

	// ---- 安全横切----
	audit := safety.NewStdAuditLog(logger)
	reg.SetAuditor(server.NewToolAuditor(audit, metrics)) // 审计 + 工具成功率指标

	// ---- 会话 / 限流 / 异步任务 ----
	// 水平扩展：REDIS_URL 配置后会话与异步任务存储都切 Redis
	// （多副本共享状态）；否则用内存实现（单机部署）。
	var sessions server.SessionStore
	var taskStore task.Store
	var rc *redis.Client
	if rurl := os.Getenv("REDIS_URL"); rurl != "" {
		rc = &redis.Client{
			Addr:     rurl,
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       atoiDefault(os.Getenv("REDIS_DB"), 0),
		}
		sessions = server.NewRedisSessionStore(rc, 30*time.Minute)
		taskStore = task.NewRedisTaskStore(rc)
		logger.Info("会话/任务存储使用 Redis（水平扩展）", "addr", redactURL(rurl))
	} else {
		sessions = server.NewInMemoryStore(30 * time.Minute)
		taskStore = task.NewInMemoryStore()                  // 异步任务存储
	}
	rate := server.NewRateLimiter(2, 5)    // 每用户每秒 2 次、突发 5 次
	fbStore := feedback.NewInMemoryStore() // 反馈闭环存储

	// Redis 长期记忆：无 Qdrant 但配了 Redis 时启用（关键词检索）
	if rc != nil && os.Getenv("QDRANT_URL") == "" {
		if rm, ok := memory.SetupManagerRedis(rc, logger); ok {
			mem = rm
			longMem = true
		}
	}

	// ---- 用户画像：对话自动学习 + 遗忘策略（TTL 保鲜 + 容量治理）----
	profileStore := memory.NewProfileStore()
	// 抽取器升级：默认 LLM 语义抽取 + 规则回退（PROFILE_LLM=0 可退回纯规则）
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
	// 定时遗忘清扫：周期执行同策略，让 TTL 过期事实被物理清理
	// 防止画像只进不出、长期运行无限膨胀。PROFILE_SWEEP_MINUTES 可调。
	sched := schedule.NewScheduler()
	sched.Every("profile-sweep", time.Duration(atoiDefault(os.Getenv("PROFILE_SWEEP_MINUTES"), 60))*time.Minute, func(ctx context.Context) {
		if n := profilePolicy.Apply(profileStore, time.Now()); n > 0 {
			logger.Info("画像定时遗忘清理", "forgotten", n)
		}
	})
	sched.Start(context.Background())

	// ---- 语音交互：OpenAI 兼容 ASR/TTS（可选，VOICE_BASE_URL 开启）----
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

	// ---- 多 Agent Supervisor：数据/知识/常规 三个专业 worker ----
	// 摘要压缩器：ZEBRA_SUMMARIZER=llm 时用 LLM 语义摘要，否则截断式。
	// SUMMARY_MAX_CHARS 摘要长度（默认 600）
	windowSummarizer := agent.Summarizer(agent.PrefixSummarizer{MaxChars: atoiDefault(os.Getenv("SUMMARY_MAX_CHARS"), 600)})
	if os.Getenv("ZEBRA_SUMMARIZER") == "llm" && router != nil {
		windowSummarizer = &agent.LLMSummarizer{Router: router, MaxChars: atoiDefault(os.Getenv("SUMMARY_MAX_CHARS"), 600)}
		logger.Info("已启用 LLM 对话摘要压缩")
	}
	var supervisorInst *supervisor.Supervisor
	if router != nil && prompts != nil {
		supervisorInst = buildSupervisor(workerDeps{
			// CONTEXT_MAX_TOKENS 上下文预算（默认 4000）；MAX_TOOL_TURNS 最大工具轮数（默认 5）
			router: router, prompts: prompts, mem: mem, window: &agent.ContextWindow{MaxTokens: atoiDefault(os.Getenv("CONTEXT_MAX_TOKENS"), 4000), Summarizer: windowSummarizer},
			moderator: moderator, skills: skillReg, cache: semanticCache, rag: ragIndex, reranker: reranker, kg: kgGraph,
			model: envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"), maxTurns: atoiDefault(os.Getenv("MAX_TOOL_TURNS"), 5), cost: costTracker, logger: logger,
		}, reg)
	}

	// ---- 配置热更新：重载 技能/提示词/知识库（不重启）----
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
				if docs, err := rag.LoadDocs("docs"); err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					for name, content := range docs {
						// RAG_CHUNK_SIZE / RAG_CHUNK_OVERLAP 文档分块参数（默认 600/100）
						if derr := ragIndex.AddDocument(ctx, content, name, atoiDefault(os.Getenv("RAG_CHUNK_SIZE"), 600), atoiDefault(os.Getenv("RAG_CHUNK_OVERLAP"), 100)); derr != nil {
							return derr
						}
					}
					logger.Info("热重载知识库", "docs", len(docs))
				}
			}
			// 4. 插件（先移除旧插件工具，再重新注册）
			for _, n := range pluginNames {
				reg.Remove(n)
			}
			pluginNames = nil
			if defs, err := plugin.Load("plugins"); err == nil && len(defs) > 0 {
				pluginNames = plugin.Register(reg, defs)
				logger.Info("热重载插件", "count", len(pluginNames))
			}
			return nil
		}
	}

	// ---- 在线评测/影子模式：候选模型 + 评审器（可选）----
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
		// Judge 独立评审模型：默认用生产 router，避免"生产模型自己评自己"
		// 的偏置时可配 JUDGE_BASE_URL / JUDGE_MODEL 指到专门的评审小模型。
		judgeRouter := router
		if jb := os.Getenv("JUDGE_BASE_URL"); jb != "" {
			var judgeProvider provider.Provider
			if os.Getenv("JUDGE_OPENAI") == "1" {
				judgeProvider = &provider.OpenAIProvider{
					BaseURL: jb, Model: envOr("JUDGE_MODEL", "gpt-4o-mini"),
					APIKey: os.Getenv("JUDGE_API_KEY"), Client: httpCli,
				}
			} else {
				judgeProvider = &provider.OllamaProvider{
					BaseURL: jb, Model: envOr("JUDGE_MODEL", "qwen3.5:0.8b-mlx"),
					Client: httpCli,
				}
			}
			judgeRouter = provider.NewRouter(judgeProvider)
			logger.Info("已启用独立评审模型", "base", jb, "model", envOr("JUDGE_MODEL", ""))
		}
		sample := float64(atoiDefault(os.Getenv("ZEBRA_SHADOW_SAMPLE"), 10)) / 100
		shadowEval = eval.NewShadowEvaluator(candidate, eval.NewJudge(judgeRouter), eval.NewShadowStore(atoiDefault(os.Getenv("SHADOW_STORE_MAX"), 200)), sample)
		shadowEval.Metrics = metrics.Inc
		shadowEval.Log = logger
		logger.Info("已启用影子评测", "candidate", shadowModel, "sample_rate", sample)
	}

	api := server.NewAPIServer(server.Deps{
		Router:        router,
		Tools:         reg,
		Prompts:       prompts,
		Mem:           mem,
		Window:        &agent.ContextWindow{MaxTokens: atoiDefault(os.Getenv("CONTEXT_MAX_TOKENS"), 4000), Summarizer: windowSummarizer},
		Moderator:     moderator,
		Audit:         audit,
		Sessions:      sessions,
		Keys:          keys,
		Rate:          rate,
		Logger:        logger,
		Metrics:       metrics,
		MaxTurns:      atoiDefault(os.Getenv("MAX_TOOL_TURNS"), 5),
		ReActMaxSteps: atoiDefault(os.Getenv("REACT_MAX_STEPS"), 6),
		PromptName:    "assistant",
		Skills:        skillReg,
		Notifier:      notifier,
		Cost:          costTracker,
		Cache:         semanticCache,
		RAG:           ragIndex,
		Reranker:      reranker,
		Model:         envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
		TaskStore:     taskStore,
		Supervisor:    supervisorInst,
		Feedback:      fbStore,
		Reload:        reload,
		Shadow:        shadowEval,
		Profile:       profileStore,
		ProfileTTL:    profilePolicy.TTL,
		Extractor:     profileExtractor,
		Voice:         voice,
		KG:            kgGraph,
		EvalCasesDir:  envOr("EVAL_CASES_DIR", "test/eval/cases"),
	})

	// ---- 启动能力清单：把"这台服务有什么"打成一目了然的终端清单 ----
	var skillsList []*skill.Skill
	if skillReg != nil {
		skillsList = skillReg.List()
	}
	var models []string
	for _, p := range router.Chain() {
		if p != nil {
			models = append(models, p.Name())
		}
	}
	shadowCandidate, shadowSample := "", 0.0
	if shadowEval != nil {
		shadowCandidate = shadowEval.CandidateName()
		shadowSample = shadowEval.SampleRate
	}
	memMode := "工作记忆"
	if longMem {
		memMode = "工作记忆 + Qdrant"
	}
	ragChunks := 0
	if ragIndex != nil {
		ragChunks = ragIndex.Len()
	}
	observe.PrintInventory(os.Stdout, observe.Info{
		Title:           "Zebra 启动清单",
		Models:          models,
		Tools:           reg,
		Skills:          skillsList,
		MCPMode:         mcpMode,
		MCPCount:        len(mcpDefs),
		MCPTools:        observe.FromMCP(mcpDefs),
		MemMode:         memMode,
		RAGDocs:         docsCount,
		RAGChunks:       ragChunks,
		VoiceEnabled:    voice != nil,
		ShadowCandidate: shadowCandidate,
		ShadowSample:    shadowSample,
		Compact:         true, // 工具/MCP/技能只显示个数，明细走 HTTP API（GET /api/v1/tools 等）
	})

	addr = envOr("ADDR", ":8080")
	if err := api.Serve(context.Background(), addr); err != nil {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}

// bindLocalOnly 判断监听地址是否仅绑定本机回环（127.0.0.1 / ::1 / localhost）。
// 空 host（如 ":8080"）绑定全部接口，视为对外暴露。
func bindLocalOnly(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// isDefaultKey 判断密钥是否仍为仓库自带的默认值：对外暴露绑定下
// 默认值等同未配置，必须拒绝启动。
func isDefaultKey(k string) bool { return k == defaultAdminKey || k == defaultUserKey }

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

// cacheThreshold 语义缓存相似度阈值（CACHE_THRESHOLD，默认 0.92）。
// 空串/非法/越界均回落默认；0~1 范围内取用户值。
func cacheThreshold() float64 {
	s := os.Getenv("CACHE_THRESHOLD")
	if s == "" {
		return 0.92
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || v > 1 {
		return 0.92
	}
	return v
}

// redactURL 启动日志脱敏：剥掉 URL 里的 userinfo（REDIS_URL /
// WEBHOOK_URL 常带密码）。剥不掉时原样返回——不能因解析失败打印空串
// 造成运维困惑。
func redactURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	parsed.User = nil
	return parsed.String()
}
