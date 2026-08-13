// 用户反馈 API（P16 反馈闭环）：
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
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
	// P55 反馈回流：负面反馈把该问答对追加进评测数据集（供回归纳入）
	if fb.Rating == feedback.RatingDown && s.deps.EvalCasesDir != "" && req.SessionID != "" {
		if sess, ok := s.deps.Sessions.Get(req.SessionID); ok {
			if q, a := latestQAPair(sess); q != "" && a != "" {
				if err := eval.AppendCase(s.deps.EvalCasesDir, eval.CaseFromFeedback(q, a, fb.Comment)); err != nil {
					s.deps.Logger.Warn("负面反馈回流失败", "err", err)
				} else {
					s.deps.Metrics.Inc("feedback:reflow")
				}
			}
		}
	}
	json.NewEncoder(w).Encode(map[string]string{"id": fb.ID, "status": "ok"})
}

// latestQAPair 取会话历史中最近的一组 用户问题 → 助手回答。
func latestQAPair(sess *Session) (string, string) {
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
