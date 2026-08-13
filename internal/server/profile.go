// 用户画像 API（P22 记忆画像/遗忘机制）。
//
//	GET  /v1/user/profile         查看自己的画像事实（TTL 之外自动隐藏）
//	POST /v1/user/profile/forget  删除一条画像事实（{key: "name"}）
//
// 画像在 Agent 对话时自动学习（规则抽取，见 internal/memory/profile.go），
// 这里只做"用户可见/可控"：画像应透明、可查、可删（GDPR 数据最小化）。
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ericthz/zebra/internal/safety"
)

// handleGetProfile 返回当前用户的画像事实（新在前）。
func (s *APIServer) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.deps.Profile == nil {
		http.Error(w, "画像未启用", http.StatusNotImplemented)
		return
	}
	facts := s.deps.Profile.FactsFor(p.User, time.Now(), s.deps.ProfileTTL)
	jsonOK(w, map[string]interface{}{
		"user":      p.User,
		"facts":     facts,
		"conflicts": s.deps.Profile.ConflictsFor(p.User), // P57 冲突消解可见
	})
}

// ProfileForgetRequest 删除某条画像事实。
type ProfileForgetRequest struct {
	Key string `json:"key"`
}

// handleForgetProfile 删除一条画像事实（比 DELETE /v1/user/data 更精细）。
func (s *APIServer) handleForgetProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.deps.Profile == nil {
		http.Error(w, "画像未启用", http.StatusNotImplemented)
		return
	}
	var req ProfileForgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		http.Error(w, "bad request: key 必填", http.StatusBadRequest)
		return
	}
	if !s.deps.Profile.ForgetKey(p.User, req.Key) {
		http.Error(w, "画像中无该事实", http.StatusNotFound)
		return
	}
	// 精细遗忘同样留痕（合规）
	if s.deps.Audit != nil {
		s.deps.Audit.Log(safety.AuditEvent{
			Time: time.Now(), User: p.User, Role: p.Role,
			Action: "profile.forget", Target: req.Key, Risk: 0, Success: true,
		})
	}
	jsonOK(w, map[string]string{"status": "forgotten", "key": req.Key})
}

// ProfileResolveRequest 裁决一条画像冲突。
type ProfileResolveRequest struct {
	Key  string `json:"key"`
	Keep string `json:"keep"` // "old"=回退旧值；其它值=保留新值
}

// handleResolveProfile 裁决画像冲突（P57）。
func (s *APIServer) handleResolveProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.deps.Profile == nil {
		http.Error(w, "画像未启用", http.StatusNotImplemented)
		return
	}
	var req ProfileResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		http.Error(w, "bad request: key 必填", http.StatusBadRequest)
		return
	}
	if !s.deps.Profile.ResolveConflict(p.User, req.Key, req.Keep == "old") {
		http.Error(w, "无该 key 的冲突", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]string{"status": "resolved", "key": req.Key, "keep": req.Keep})
}
