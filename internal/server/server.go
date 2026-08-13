// A1 服务装配：路由 + 中间件 + 优雅停机（B8）。
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/cost"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// Deps 服务依赖（全部可替换，方便测试与生产替换实现）。
type Deps struct {
	Router     *provider.Router    // C15 多模型路由
	Tools      *tool.Registry      // D20 工具权限
	Prompts    *prompt.Registry    // C16 提示词
	Mem        *memory.Manager     // C12 记忆
	Window     *agent.ContextWindow // C11 上下文工程
	Moderator  safety.Moderator    // D18 内容审核
	Audit      safety.AuditLog     // D20 审计
	Sessions   SessionStore        // A2 会话
	Keys       *KeyStore           // A3 API Key
	Rate       *RateLimiter        // B6 限流
	Logger     *slog.Logger        // B5 日志
	Metrics    *Metrics            // B5 指标
	MaxTurns   int
	PromptName string
	Skills     *skill.Registry // P1 技能注册表（nil 关闭技能检索）
	Notifier   notify.Notifier // P4 主动出站：任务完成通知（nil 关闭）
	Cost       *cost.Tracker   // P5 成本归因（nil 关闭）
	Cache      *cache.SemanticCache // P5 语义缓存（nil 关闭）
	Model      string          // 主模型名（成本归因用）
}

// APIServer HTTP 服务。
type APIServer struct {
	deps Deps
}

// NewAPIServer 构造。
func NewAPIServer(deps Deps) *APIServer {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Metrics == nil {
		deps.Metrics = NewMetrics()
	}
	return &APIServer{deps: deps}
}

// agentFor 为会话创建并绑定 Agent（每会话一个实例，共享只读依赖）。
func (s *APIServer) agentFor(sess *Session) *agent.Agent {
	ag := agent.New(agent.Config{
		Router:     s.deps.Router,
		Tools:      s.deps.Tools,
		Prompts:    s.deps.Prompts,
		Mem:        s.deps.Mem,
		Window:     s.deps.Window,
		Moderator:  s.deps.Moderator,
		MaxTurns:   s.deps.MaxTurns,
		PromptName: s.deps.PromptName,
		Skills:     s.deps.Skills,
		Cache:      s.deps.Cache,
		Model:      s.deps.Model,
		OnUsage: func(model string, in, out int) { // B5 用量指标 + P5 成本归因
			s.deps.Metrics.Inc("tokens_in:" + itoa(in/100))
			s.deps.Metrics.Inc("tokens_out:" + itoa(out/100))
			if s.deps.Cost != nil {
				s.deps.Cost.Record(sess.User, sess.ID, model, in, out)
			}
		},
	})
	return ag.Bind(sess.ID, sess.Role, sess.User, sess.History())
}

// Handler 装配全部路由与中间件。
func (s *APIServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", s.handleChat)
	mux.HandleFunc("/v1/chat/stream", s.handleChatStream)
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
	h = Auth(s.deps.Keys, "/healthz", "/readyz", "/metrics", "/metrics/cost")(h)
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
