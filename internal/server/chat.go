// A1 对话 API：/v1/chat（JSON）与 /v1/chat/stream（SSE）。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/supervisor"
)

// ChatRequest 请求体。
type ChatRequest struct {
	SessionID    string `json:"session_id,omitempty"`    // 空则新建会话
	Message      string `json:"message"`                 // 用户输入
	Stream       bool   `json:"stream,omitempty"`        // 是否流式
	ConfirmRisky bool   `json:"confirm_risky,omitempty"` // D20 高危工具二次确认授权
	Mode         string `json:"mode,omitempty"`          // "plan"=规划-执行(P10)；"reflect"=反思(P40)；空=普通执行
	Shadow       bool   `json:"shadow,omitempty"`        // P21 显式触发影子评测（默认按采样率）
}

// ChatResponse 非流式响应。
type ChatResponse struct {
	SessionID string `json:"session_id"`
	Reply     string `json:"reply"`
	Shadow    bool   `json:"shadow_sampled,omitempty"` // P21 是否进了影子对比
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
	// P10 编排模式：先规划再逐步执行；P13 多 Agent：自动路由到专业 Worker
	var reply string
	switch req.Mode {
	case "plan":
		reply, err = ag.PlanAndExecute(ctx, req.Message, opts)
	case "supervisor":
		if s.deps.Supervisor == nil {
			http.Error(w, "supervisor 未启用", http.StatusNotImplemented)
			return
		}
		var workerName string
		var w *supervisor.Worker
		reply, w, err = s.deps.Supervisor.Run(ctx, req.Message, sess.ID, sess.Role, sess.User, sess.History(), opts)
		if w != nil {
			workerName = w.Name
		}
		s.deps.Logger.Info("supervisor 路由", "worker", workerName)
	case "reflect":
		// P40 反思：先正常回答，再让模型批判-改进一轮（失败自动回退原回答）
		reply, err = ag.Run(ctx, req.Message, opts)
		if err == nil {
			reply, err = ag.Reflect(ctx, req.Message, reply)
		}
	default:
		reply, err = ag.Run(ctx, req.Message, opts)
	}
	if err != nil {
		s.deps.Logger.Warn("chat failed", "session", sess.ID, "err", err)
		http.Error(w, "agent error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := ChatResponse{SessionID: sess.ID, Reply: reply}

	// P28 水平扩展：Redis 会话存储需要把 Agent 修改后的历史写回，
	// 否则下一轮请求打到其它副本时读不到多轮上下文。
	s.persistHistory(sess)

	// P21 影子模式：真实流量按采样率（或显式请求）复制给候选模型对比。
	// 异步执行，不阻塞用户响应；结论落影子记录，供换模型前的回归评估。
	if s.deps.Shadow != nil && s.deps.Shadow.WantSample(req.Shadow) {
		resp.Shadow = true
		go func() {
			res := s.deps.Shadow.Run(context.Background(), sess.User, sess.ID, req.Message, reply, s.deps.Model)
			s.deps.Logger.Info("shadow sampled", "id", res.ID, "verdict", res.Verdict)
		}()
	}
	json.NewEncoder(w).Encode(resp)

	// P4 主动出站：任务完成异步通知业务系统（不阻塞响应）
	if s.deps.Notifier != nil {
		go func() {
			ev := notify.Event{
				Type: "task.complete", Session: sess.ID, Title: "对话完成",
				Payload: map[string]any{"user": sess.User, "reply_summary": truncateText(reply, 100)},
			}
			if err := s.deps.Notifier.Send(context.Background(), ev); err != nil {
				s.deps.Logger.Warn("webhook 通知失败", "session", sess.ID, "err", err)
			}
		}()
	}
}

// truncateText 截断摘要，避免把完整回复推给业务系统。
func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return s
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
		s.persistHistory(sess)
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
	// P28：流式对话结束后同样写回历史
	s.persistHistory(sess)
}

// persistHistory 若会话存储实现了 HistoryPersister，把最新历史写回。
func (s *APIServer) persistHistory(sess *Session) {
	if hp, ok := s.deps.Sessions.(HistoryPersister); ok {
		if err := hp.Save(sess); err != nil {
			s.deps.Logger.Warn("会话历史写回失败", "session", sess.ID, "err", err)
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
