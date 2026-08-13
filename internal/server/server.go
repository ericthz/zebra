// A1 服务装配：路由 + 中间件 + 优雅停机（B8）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/cost"
	"github.com/ericthz/zebra/internal/eval"
	"github.com/ericthz/zebra/internal/feedback"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/supervisor"
	"github.com/ericthz/zebra/internal/task"
	"github.com/ericthz/zebra/internal/tool"
)

// Deps 服务依赖（全部可替换，方便测试与生产替换实现）。
type Deps struct {
	Router     *provider.Router     // C15 多模型路由
	Tools      *tool.Registry       // D20 工具权限
	Prompts    *prompt.Registry     // C16 提示词
	Mem        *memory.Manager      // C12 记忆
	Window     *agent.ContextWindow // C11 上下文工程
	Moderator  safety.Moderator     // D18 内容审核
	Audit      safety.AuditLog      // D20 审计
	Sessions   SessionStore         // A2 会话
	Keys       *KeyStore            // A3 API Key
	Rate       *RateLimiter         // B6 限流
	Logger     *slog.Logger         // B5 日志
	Metrics    *Metrics             // B5 指标
	MaxTurns   int
	PromptName string
	Skills     *skill.Registry         // P1 技能注册表（nil 关闭技能检索）
	Notifier   notify.Notifier         // P4 主动出站：任务完成通知（nil 关闭）
	Cost       *cost.Tracker           // P5 成本归因（nil 关闭）
	Cache      *cache.SemanticCache    // P5 语义缓存（nil 关闭）
	RAG        *rag.Index              // P8 知识库检索（nil 关闭）
	Model      string                  // 主模型名（成本归因用）
	TaskStore  task.Store              // P12 异步任务存储（nil 关闭异步 API）
	Supervisor *supervisor.Supervisor  // P13 多 Agent（nil 关闭 supervisor 模式）
	Feedback   *feedback.InMemoryStore // P16 反馈闭环（nil 关闭反馈 API）
	Reload     func() error            // P18 配置热更新（nil 关闭重载端点）
	Shadow     *eval.ShadowEvaluator   // P21 在线评测/影子模式（nil 关闭）
	Profile    *memory.ProfileStore    // P22 用户画像（nil 关闭画像 API 与注入）
	ProfileTTL time.Duration           // P22 画像事实保鲜期（<=0 永不过期）
	Extractor  memory.Extractor        // P27 画像抽取器（nil 用规则抽取）
	Voice      *provider.VoiceClient   // P25 语音交互（nil 关闭语音 API）
}

// APIServer HTTP 服务。
type APIServer struct {
	deps  Deps
	tasks *task.Manager // P12 异步任务管理器（NewAPIServer 时构建）

	// P42 金丝雀自动回滚：记录 promote 时的原主模型名，质量回退时切回。
	shadowMu   sync.Mutex
	shadowPrev string
}

// NewAPIServer 构造。
func NewAPIServer(deps Deps) *APIServer {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Metrics == nil {
		deps.Metrics = NewMetrics()
	}
	api := &APIServer{deps: deps}
	// P12 异步任务：有存储则构建管理器（run 由 APIServer 提供，可访问 buildAgent）
	if deps.TaskStore != nil {
		api.tasks = task.NewManager(deps.TaskStore, api.taskRun, 4)
		api.tasks.SetNotify(api.makeTaskNotifier())
		api.tasks.Start()
	}
	return api
}

// buildAgent 用指定会话上下文构造 Agent（history 可来自会话或任务检查点）。
// 每 Agent 共享只读依赖；历史按会话/任务隔离（A4）。
func (s *APIServer) buildAgent(user, role, sessionID string, hist *[]provider.Message) *agent.Agent {
	ag := agent.New(agent.Config{
		Router:       s.deps.Router,
		Tools:        s.deps.Tools,
		Prompts:      s.deps.Prompts,
		Mem:          s.deps.Mem,
		Window:       s.deps.Window,
		Moderator:    s.deps.Moderator,
		MaxTurns:     s.deps.MaxTurns,
		PromptName:   s.deps.PromptName,
		Skills:       s.deps.Skills,
		Cache:        s.deps.Cache,
		RAG:          s.deps.RAG,
		Profile:      s.deps.Profile,
		ProfileTTL:   s.deps.ProfileTTL,
		Extractor:    s.deps.Extractor,
		Model:        s.deps.Model,
		RewriteQuery: os.Getenv("ZEBRA_QUERY_REWRITE") == "1", // P48 查询改写
		OnSkill: func(names []string) { // P31：技能注入可观测
			s.deps.Logger.Info("skill.inject", "session", sessionID, "user", user,
				"skills", strings.Join(names, ","))
		},
		OnTool: func(name string, args map[string]interface{}, ok bool, err error) { // P31：工具调用可观测
			detail, _ := json.Marshal(args)
			msg := "ok"
			if err != nil {
				msg = err.Error()
			}
			s.deps.Logger.Info("tool.call", "session", sessionID, "user", user,
				"tool", name, "args", safety.Redact(string(detail)), "ok", ok, "err", msg)
		},
		OnUsage: func(model string, in, out int) { // B5 用量指标 + P5 成本归因
			s.deps.Metrics.Inc("tokens_in:" + itoa(in/100))
			s.deps.Metrics.Inc("tokens_out:" + itoa(out/100))
			if s.deps.Cost != nil {
				s.deps.Cost.Record(user, sessionID, model, in, out)
			}
		},
	})
	return ag.Bind(sessionID, role, user, hist)
}

// agentFor 为会话创建并绑定 Agent（历史指向会话自身的共享切片）。
func (s *APIServer) agentFor(sess *Session) *agent.Agent {
	return s.buildAgent(sess.User, sess.Role, sess.ID, sess.History())
}

// Handler 装配全部路由与中间件。
func (s *APIServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", s.handleChat)
	mux.HandleFunc("/v1/chat/stream", s.handleChatStream)
	mux.HandleFunc("DELETE /v1/user/data", s.handleForget) // P6 被遗忘权
	if s.tasks != nil {
		mux.HandleFunc("POST /v1/tasks", s.handleSubmitTask) // P12 异步任务
		mux.HandleFunc("GET /v1/tasks", s.handleListTasks)   // P12 任务列表
		mux.HandleFunc("GET /v1/tasks/", s.handleGetTask)    // P12 任务查询
	}
	if s.deps.Feedback != nil {
		mux.HandleFunc("POST /v1/feedback", s.handleSubmitFeedback) // P16 反馈
		mux.HandleFunc("GET /v1/feedback", s.handleListFeedback)    // P16 反馈列表
	}
	if s.deps.Reload != nil {
		mux.HandleFunc("POST /v1/admin/reload", s.handleReload) // P18 热更新（仅 admin）
	}
	if s.deps.Shadow != nil {
		mux.HandleFunc("POST /v1/eval/shadow", s.handleRunShadow)             // P21 影子评测
		mux.HandleFunc("GET /v1/eval/shadow", s.handleListShadow)             // P21 影子记录
		mux.HandleFunc("GET /v1/eval/shadow/stats", s.handleShadowStats)      // P26 影子看板
		mux.HandleFunc("POST /v1/eval/shadow/promote", s.handleShadowPromote) // P26 灰度切换
	}
	if s.deps.Profile != nil {
		mux.HandleFunc("GET /v1/user/profile", s.handleGetProfile)            // P22 画像查看
		mux.HandleFunc("POST /v1/user/profile/forget", s.handleForgetProfile) // P22 精细遗忘
	}
	if s.deps.Voice != nil {
		mux.HandleFunc("POST /v1/voice/transcribe", s.handleVoiceTranscribe) // P25 ASR
		mux.HandleFunc("POST /v1/voice/synthesize", s.handleVoiceSynthesize) // P25 TTS
		mux.HandleFunc("POST /v1/voice/chat", s.handleVoiceChat)             // P25 语音对话
	}
	mux.HandleFunc("/", uiHandler()) // P14 前端 Web UI（公开）
	mux.HandleFunc("/healthz", HealthzHandler())
	mux.HandleFunc("/readyz", ReadyzHandler(s.deps.Logger, map[string]func() error{
		"llm":   func() error { return s.toolsReadyCheck() },
		"tools": func() error { return s.toolsReadyCheck() },
	}))
	mux.Handle("/metrics", s.deps.Metrics.Handler())
	if s.deps.Cost != nil {
		mux.Handle("/metrics/cost", s.deps.Cost.Handler())
	}

	// 鉴权 + 限流 + 日志 + 恢复，按序包裹业务路由
	var h http.Handler = mux
	h = Recover(s.deps.Logger)(h)
	h = AccessLog(s.deps.Logger, s.deps.Metrics)(h)
	h = RateLimit(s.deps.Rate)(h)
	h = Auth(s.deps.Keys, "/", "/healthz", "/readyz", "/metrics", "/metrics/cost")(h)
	h = RequestID(h)
	return h
}

// Serve 启动服务并阻塞，处理信号实现优雅停机（B8）。
func (s *APIServer) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// 信号监听
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		s.deps.Logger.Info("server listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done(): // 收到中断/超时 → 优雅停机
		s.deps.Logger.Info("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if st, ok := s.deps.Sessions.(*InMemoryStore); ok {
			st.Stop() // 停止会话后台清理
		}
		if s.tasks != nil {
			s.tasks.Stop() // 停止异步任务消费者（等待在途任务完成）
		}
		s.deps.Logger.Info("shutdown complete")
		return nil
	}
}

// toolsReadyCheck 供 readyz 复用（避免未使用告警）。
func (s *APIServer) toolsReadyCheck() error {
	if s.deps.Tools == nil {
		return errors.New("tools registry not initialized")
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
