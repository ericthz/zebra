// 用户反馈 API（反馈闭环）：
//
//	POST /v1/feedback   提交点赞/踩（{session_id, rating: 1|-1, comment?}）
//	GET  /v1/feedback   列出当前用户反馈 + 正负计数
//
// 反馈同时：
//   - 落反馈存储（供回流评测集/复盘）
//   - 记入 /metrics（feedback:positive / feedback:negative，可告警）
//   - 写审计（谁在何时对哪个会话打分，合规留痕）
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ericthz/zebra/internal/eval"
	"github.com/ericthz/zebra/internal/feedback"
	"github.com/ericthz/zebra/internal/safety"
)

// FeedbackRequest 反馈请求体。
type FeedbackRequest struct {
	SessionID string `json:"session_id"`
	Rating    int    `json:"rating"` // 1 赞 / -1 踩
	Comment   string `json:"comment,omitempty"`
}

// handleSubmitFeedback 提交反馈。
func (s *APIServer) handleSubmitFeedback(w http.ResponseWriter, r *http.Request) {
	var req FeedbackRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	fb := &feedback.Feedback{User: p.User, Session: req.SessionID, Rating: req.Rating, Comment: req.Comment}
	if err := s.deps.Feedback.Add(fb); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// 指标：正/负反馈计数（可在 /metrics 观察，设阈值告警"差评率过高"）
	label := "negative"
	if fb.Rating == feedback.RatingUp {
		label = "positive"
	}
	s.deps.Metrics.Inc("feedback:" + label)

	// 审计留痕
	if s.deps.Audit != nil {
		s.deps.Audit.Log(safety.AuditEvent{
			Time: time.Now(), User: p.User, Action: "feedback.submit", Target: req.SessionID,
			Detail: safety.Redact(req.Comment), Risk: 0, Success: true,
		})
	}
	// 反馈回流：负面反馈把该问答对追加进评测数据集（供回归纳入）
	if fb.Rating == feedback.RatingDown && s.deps.EvalCasesDir != "" && req.SessionID != "" {
		if q, a := s.latestQAPairForUser(p, req.SessionID); q != "" && a != "" {
			if err := eval.AppendCase(s.deps.EvalCasesDir, eval.CaseFromFeedback(q, a, fb.Comment)); err != nil {
				s.deps.Logger.Warn("负面反馈回流失败", "err", err)
			} else {
				s.deps.Metrics.Inc("feedback:reflow")
			}
		}
	}
	json.NewEncoder(w).Encode(map[string]string{"id": fb.ID, "status": "ok"})
}

// latestQAPairForUser 取"当前用户"会话中最近一组 用户问题 → 助手回答。
// 归属校验（六4 越权）：会话属于他人时返回空，防止把他人对话落进
// 评测数据集；并在会话执行锁内重取最新历史，避免与并发 Agent 写竞争
// （无锁读 *sess.History() 是数据竞争）。
func (s *APIServer) latestQAPairForUser(p Principal, sessionID string) (string, string) {
	sess, ok := s.deps.Sessions.Get(sessionID)
	if !ok {
		return "", ""
	}
	if sess.Tenant != p.Tenant || sess.User != p.User { // 归属校验
		return "", ""
	}
	sess.runMu.Lock() // 与 /v1/chat 同款执行锁：锁内重取最新历史
	defer sess.runMu.Unlock()
	if fresh, ok := s.deps.Sessions.Get(sessionID); ok {
		sess = fresh
	}
	h := *sess.History()
	for i := len(h) - 1; i >= 1; i-- {
		if h[i].Role == "assistant" && h[i-1].Role == "user" {
			return h[i-1].Content, h[i].Content
		}
	}
	return "", ""
}

// handleListFeedback 列出当前用户的反馈。
func (s *APIServer) handleListFeedback(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	pos, neg := s.deps.Feedback.Counts()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"feedback": s.deps.Feedback.List(p.User),
		"counts":   map[string]int64{"positive": pos, "negative": neg},
	})
}
