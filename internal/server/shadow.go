// 在线评测 / 影子模式 API（P21）。
//
//	POST /v1/eval/shadow  触发一次影子评测（同步返回主回答 + 影子对比结论）
//	GET  /v1/eval/shadow   列出影子记录（运维视图）
//
// 影子模式把"真实流量"复制给候选模型：主模型照常回答用户，候选模型
// 独立回答同一问题，后台双评对比 —— 换模型前先积累回归数据，而不是拍脑袋切。
// 本端点属于运维能力，仅 admin 可用（与 P18 热更新同级）。
package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ericthz/zebra/internal/agent"
)

// ShadowRequest 触发一次影子评测。
type ShadowRequest struct {
	SessionID string `json:"session_id,omitempty"` // 空则新建会话
	Message   string `json:"message"`              // 原始用户问题
}

// handleRunShadow 同步跑一次影子评测：
// 主模型出答案（走正常 Agent 流程）→ 候选模型独立回答 → Judge 双评对比。
func (s *APIServer) handleRunShadow(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if p.Role != "admin" { // RBAC：评测属运维动作
		http.Error(w, "仅 admin 可触发影子评测", http.StatusForbidden)
		return
	}
	if s.deps.Shadow == nil {
		http.Error(w, "影子评测未启用（需配置 ZEBRA_SHADOW_MODEL）", http.StatusNotImplemented)
		return
	}
	var req ShadowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		http.Error(w, "bad request: message 必填", http.StatusBadRequest)
		return
	}

	sess, err := s.sessionFor(r.Context(), req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	// 1. 主模型走正常 Agent 流程（与 /v1/chat 等价）
	ag := s.agentFor(sess)
	reply, err := ag.Run(r.Context(), req.Message, agent.RunOptions{})
	if err != nil {
		s.deps.Logger.Warn("shadow primary failed", "session", sess.ID, "err", err)
		http.Error(w, "agent error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 2. 影子对比（同步：评测请求可等待完整结论）
	res := s.deps.Shadow.Run(r.Context(), sess.User, sess.ID, req.Message, reply, s.deps.Model)
	jsonOK(w, map[string]interface{}{
		"reply":  reply,
		"shadow": res,
	})
}

// handleListShadow 列出影子评测记录（新在前，limit 默认 20）。
func (s *APIServer) handleListShadow(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if p.Role != "admin" {
		http.Error(w, "仅 admin 可查看影子评测", http.StatusForbidden)
		return
	}
	if s.deps.Shadow == nil {
		http.Error(w, "影子评测未启用", http.StatusNotImplemented)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	jsonOK(w, map[string]interface{}{"runs": s.deps.Shadow.Store.Recent("", limit)})
}
