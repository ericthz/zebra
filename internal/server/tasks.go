// 异步长任务 API（P12）：
//
//	POST /v1/tasks      提交任务（立即返回 task_id，后台执行）
//	GET  /v1/tasks      列出当前用户的全部任务
//	GET  /v1/tasks/{id} 查询单个任务（status/progress/result）
//
// 检查点（checkpoint）机制：
//
//	任务执行前/后，把会话历史序列化为快照存进 Task.Checkpoint；
//	恢复/继续时反序列化回 history，实现"断点续跑"。
//
// 完成/失败时触发 Webhook 通知（复用 P4）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/notify"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/task"
)

// TaskSubmitRequest 提交任务请求。
type TaskSubmitRequest struct {
	SessionID string `json:"session_id,omitempty"` // 空则新建会话
	Message   string `json:"message"`
}

// handleSubmitTask 提交异步任务：立即返回 task_id，后台执行。
func (s *APIServer) handleSubmitTask(w http.ResponseWriter, r *http.Request) {
	var req TaskSubmitRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "bad request: message 必填", http.StatusBadRequest)
		return
	}
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	sess, err := s.sessionFor(r.Context(), req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	id, err := s.tasks.Submit(p.User, sess.ID, req.Message)
	if err != nil {
		if errors.Is(err, task.ErrQueueFull) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		s.writeAgentError(w, "任务提交失败", err)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"task_id": id, "session_id": sess.ID})
}

// taskRun 由 Manager 注入的任务执行函数：
// 反序列化检查点 → 构建 Agent → 执行 → 序列化新检查点。
func (s *APIServer) taskRun(ctx context.Context, user, session, prompt string, cp []byte) (string, []byte, error) {
	// F-5：以会话现有历史为"上文"基础（"继续上一条分析"类任务需要读到
	// 该会话已有多轮对话）。防御性拷贝，避免与并发聊天写历史竞态。
	hist := make([]provider.Message, 0)
	if sess, ok := s.deps.Sessions.Get(session); ok {
		hist = append(hist, (*sess.History())...)
	}
	if len(cp) > 0 {
		// 检查点恢复（断点续跑）。解析失败不得静默当空历史重跑——
		// 那会"丢失上文"继续执行（违背断点续跑语义），应让任务失败
		// 便于排查（P2-13）。
		if err := json.Unmarshal(cp, &hist); err != nil {
			return "", nil, fmt.Errorf("检查点解析失败: %w", err)
		}
	}
	role := "user"
	if sess, ok := s.deps.Sessions.Get(session); ok {
		role = sess.Role
	}
	ag := s.buildAgent(user, role, session, &hist)
	reply, err := ag.Run(ctx, prompt, agent.RunOptions{})
	cpOut, _ := json.Marshal(hist) // 新检查点
	return reply, cpOut, err
}

// handleListTasks 列出当前用户的全部任务。
func (s *APIServer) handleListTasks(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tasks := s.tasks.List(p.User)
	// 脱敏：不返回 prompt/检查点，只返回可公开字段
	type brief struct {
		ID, Status, Progress string
		Result, Error        string
		Created              time.Time
	}
	out := make([]brief, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, brief{t.ID, t.Status, t.Progress, t.Result, t.Error, t.Created})
	}
	json.NewEncoder(w).Encode(out)
}

// handleGetTask 查询单个任务。
func (s *APIServer) handleGetTask(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	if id == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}
	t, ok := s.tasks.Get(id)
	if !ok || t.User != p.User { // A4 隔离：只能查自己的任务
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{
		"id": t.ID, "status": t.Status, "progress": t.Progress,
		"result": t.Result, "error": t.Error, "session": t.Session,
	})
}

// makeTaskNotifier 构造任务完成通知器（复用 P4 Webhook，发送完成事件）。
func (s *APIServer) makeTaskNotifier() task.NotifyFunc {
	return func(t *task.Task) {
		if s.deps.Notifier == nil {
			return
		}
		ev := notify.Event{
			Type: "task.complete", Session: t.Session, Title: "异步任务完成",
			Payload: map[string]any{
				"task_id": t.ID, "status": t.Status,
				"result_summary": truncateText(t.Result, 100),
			},
		}
		if err := s.deps.Notifier.Send(context.Background(), ev); err != nil {
			s.deps.Logger.Warn("任务完成通知失败", "task", t.ID, "err", err)
		}
	}
}
