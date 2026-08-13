// B5 可观测性横切：结构化日志 + 请求 ID + panic 恢复。
// 同时承载 A3（认证注入）与 B6（限流）两个横切点，集中在这里装配。
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxPrincipal
)

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
