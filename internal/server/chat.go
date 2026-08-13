// A1 对话 API：/v1/chat（JSON）与 /v1/chat/stream（SSE）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ericthz/zebra/internal/agent"
)

// ChatRequest 请求体。
type ChatRequest struct {
	SessionID     string `json:"session_id,omitempty"` // 空则新建会话
	Message       string `json:"message"`              // 用户输入
	Stream        bool   `json:"stream,omitempty"`     // 是否流式
	ConfirmRisky  bool   `json:"confirm_risky,omitempty"` // D20 高危工具二次确认授权
}

// ChatResponse 非流式响应。
type ChatResponse struct {
	SessionID string `json:"session_id"`
	Reply     string `json:"reply"`
}

// handleChat 非流式对话。
func (s *APIServer) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	sess, err := s.sessionFor(ctx, req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	ag := s.agentFor(sess)
	opts := agent.RunOptions{}
	if req.ConfirmRisky {
		opts.Confirm = func(string, map[string]interface{}) bool { return true }
	}
	reply, err := ag.Run(ctx, req.Message, opts)
	if err != nil {
		s.deps.Logger.Warn("chat failed", "session", sess.ID, "err", err)
		http.Error(w, "agent error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(ChatResponse{SessionID: sess.ID, Reply: reply})
}

// handleChatStream SSE 流式：事件逐条推给客户端（C10）。
func (s *APIServer) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	sess, err := s.sessionFor(ctx, req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	ag := s.agentFor(sess)
	opts := agent.RunOptions{}
	if req.ConfirmRisky {
		opts.Confirm = func(string, map[string]interface{}) bool { return true }
	}

	// SSE 头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	// 先回传 session_id（客户端据此续接会话）
	fmt.Fprintf(w, "event: session\ndata: %s\n\n", sess.ID)
	if flusher != nil {
		flusher.Flush()
	}

	evs, err := ag.RunStream(ctx, req.Message, opts)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err)
		return
	}
	for ev := range evs {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// sessionFor 获取或创建会话，并刷新 TTL（A2）。
func (s *APIServer) sessionFor(ctx context.Context, sessionID string) (*Session, error) {
	p, ok := principal(ctx)
	if !ok {
		return nil, fmt.Errorf("missing principal")
	}
	if sessionID != "" {
		if sess, ok := s.deps.Sessions.Get(sessionID); ok {
			if sess.Tenant == p.Tenant && sess.User == p.User { // A4 会话归属校验
				s.deps.Sessions.Touch(sessionID)
				return sess, nil
			}
			return nil, fmt.Errorf("session 不属于当前用户")
		}
		return nil, fmt.Errorf("session 不存在或已过期")
	}
	sess, err := s.deps.Sessions.Create(p.User, p.Tenant, p.Role, 30*time.Minute)
	if err != nil {
		return nil, err
	}
	return sess, nil
}
