// B5 可观测性横切：结构化日志 + 请求 ID + panic 恢复。
// 同时承载 A3（认证注入）与 B6（限流）两个横切点，集中在这里装配。
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/ericthz/zebra/internal/agent"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxPrincipal
)

// maxJSONBody 请求体上限（六7）：所有 JSON handler 统一封顶，防止
// 恶意大 body 让 Decode 吃光内存。multipart 音频另有独立上限。
const maxJSONBody = 1 << 20 // 1MB

// decodeJSON 限量解码 JSON 请求体（六7）。封顶后 Decode 因超限报错，
// 统一返回 413 而不是 400。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	err := json.NewDecoder(r.Body).Decode(dst)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "请求体过大", http.StatusRequestEntityTooLarge)
			return err
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return err
	}
	return nil
}

// RequestID 生成或透传请求 ID（用于链路追踪；生产可扩展 OpenTelemetry trace）。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

// AccessLog 结构化访问日志：记录请求 ID、身份、路径、状态、耗时。
func AccessLog(logger *slog.Logger, metrics *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)
			dur := time.Since(start)
			metrics.Observe("http_request_duration", dur)

			user := ""
			if p, ok := r.Context().Value(ctxPrincipal).(Principal); ok {
				user = p.User
			}
			logger.Info("http",
				"request_id", requestID(r.Context()),
				"user", user, "method", r.Method, "path", r.URL.Path,
				"status", rec.status, "duration_ms", dur.Milliseconds(),
			)
		})
	}
}

// Recover panic 恢复：请求级兜底，不拖垮整个服务（B9 错误恢复）。
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered", "request_id", requestID(r.Context()),
						"err", err, "stack", string(debug.Stack()))
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Auth 认证中间件：校验 Bearer Key，注入 Principal；失败返回 401。
// public 参数列出免鉴权路径（如 /healthz /readyz /metrics，供探针/采集器访问）。
func Auth(keys *KeyStore, public ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range public {
				if r.URL.Path == p {
					next.ServeHTTP(w, r)
					return
				}
			}
			bearer := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(bearer) <= len(prefix) || bearer[:len(prefix)] != prefix {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			p, ok := keys.Authenticate(bearer[len(prefix):])
			if !ok {
				http.Error(w, "invalid api key", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p)))
		})
	}
}

// RateLimit 限流中间件：按用户身份限流，超限返回 429（B6）。
func RateLimit(lim *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "anonymous"
			if p, ok := r.Context().Value(ctxPrincipal).(Principal); ok {
				key = p.User
			}
			if !lim.Allow(key) {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// principal 从上下文取认证主体。
func principal(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(Principal)
	return p, ok
}

// requireAdmin 管理员专用（P1-9）：须先经 Auth（principal 已注入），
// 非 admin 角色一律 403。用于 /metrics/cost 等含跨用户明细的敏感端点。
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := principal(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if p.Role != "admin" {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// userFacingError 透传面向用户的错误文案；其余一律回通用文案（P1-10）：
// 内部错误细节（provider URL、堆栈、内部路径）只进日志，绝不外泄给客户端。
func userFacingError(err error) string {
	var ue *agent.UserFacingError
	if errors.As(err, &ue) {
		return ue.Msg
	}
	return "internal error"
}

// writeAgentError 统一写 Agent 相关 5xx：先记日志（保留内部细节），再按
// userFacingError 规则回客户端。
func (s *APIServer) writeAgentError(w http.ResponseWriter, logMsg string, err error) {
	s.deps.Logger.Warn(logMsg, "err", err)
	http.Error(w, userFacingError(err), http.StatusInternalServerError)
}

func requestID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxRequestID).(string); ok {
		return id
	}
	return "-"
}

// statusRecorder 捕获响应状态码，供日志与指标使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush 透传 SSE 流式刷新（六3）：无此方法时 w.(http.Flusher) 断言恒失败，
// /v1/chat/stream 的事件会被 HTTP 层缓冲、一次性吐出，实时流式失效。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
