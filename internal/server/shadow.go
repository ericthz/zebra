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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/eval"
	"github.com/ericthz/zebra/internal/safety"
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
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, "bad request: message 必填", http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "bad request: message 必填", http.StatusBadRequest)
		return
	}

	sess, err := s.lockSession(r.Context(), req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	defer sess.runMu.Unlock()
	// 1. 主模型走正常 Agent 流程（与 /v1/chat 等价）
	ag := s.agentFor(sess)
	reply, err := ag.Run(r.Context(), req.Message, agent.RunOptions{})
	if err != nil {
		// 失败轮次 Agent 已改写内存历史，仍须落库（六10，与 /v1/chat 对齐）
		s.persistHistory(sess)
		s.writeAgentError(w, "shadow primary failed", err)
		return
	}
	// 影子评测同样写回会话历史（Redis 会话下多轮不丢，与 /v1/chat 对齐）
	s.persistHistory(sess)
	// 2. 影子对比（同步：评测请求可等待完整结论）
	res := s.deps.Shadow.Run(r.Context(), sess.User, sess.ID, req.Message, reply, s.deps.Model)
	s.maybeShadowRollback() // P42：金丝雀质量回退自动切回
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

// handleShadowStats 影子评测看板：聚合统计 + 灰度切换建议（P26）。
func (s *APIServer) handleShadowStats(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if p.Role != "admin" {
		http.Error(w, "仅 admin 可查看影子看板", http.StatusForbidden)
		return
	}
	if s.deps.Shadow == nil {
		http.Error(w, "影子评测未启用", http.StatusNotImplemented)
		return
	}
	stats := s.deps.Shadow.Store.Stats("")
	// 默认策略：样本 >= 10、候选胜率 >= 60% 才建议切换（可配置化扩展）
	rec := eval.RecommendSwitch(stats, 10, 60)
	jsonOK(w, map[string]interface{}{
		"stats":          stats,
		"recommendation": rec,
		"candidate":      s.deps.Shadow.CandidateName(),
		"primary":        s.deps.Router.Primary().Name(),
	})
}

// handleShadowPromote 灰度切换：把候选模型提升为主模型（仅 admin，即时生效）。
func (s *APIServer) handleShadowPromote(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if p.Role != "admin" {
		http.Error(w, "仅 admin 可切换主模型", http.StatusForbidden)
		return
	}
	if s.deps.Shadow == nil {
		http.Error(w, "影子评测未启用", http.StatusNotImplemented)
		return
	}
	name := s.deps.Shadow.CandidateName()
	prev, ok := s.deps.Router.Promote(name)
	if !ok {
		http.Error(w, "候选模型未在路由链中: "+name, http.StatusNotFound)
		return
	}
	// P42 候选提升为新主后，把影子候选重指向原主模型：
	// 否则候选 == 新主 → 影子变成"新主 vs 自己"的自我对比，胜率数据被污染。
	// 重指向后，影子持续对比"新主 vs 原主"，为金丝雀回滚提供真实信号。
	if next := s.deps.Router.Get(prev); next != nil {
		s.deps.Shadow.SetCandidate(next)
	}
	// 记录原主模型与本次提升的模型，供金丝雀自动回滚（P42）
	s.shadowMu.Lock()
	s.shadowPrev = prev
	s.shadowPromoted = name
	s.shadowMu.Unlock()
	// 切换是重要运维动作，必须审计留痕
	if s.deps.Audit != nil {
		s.deps.Audit.Log(safety.AuditEvent{
			Time: time.Now(), User: p.User, Role: p.Role,
			Action: "shadow.promote", Target: name, Risk: 2, Success: true,
		})
	}
	s.deps.Logger.Info("影子候选已提升为主模型", "user", p.User, "model", name)
	jsonOK(w, map[string]interface{}{"status": "promoted", "primary": name})
}

// maybeShadowRollback 金丝雀自动回滚（P42）：promote 后影子候选已重指向
// 原主模型，影子持续对比"新主 vs 原主"。当原主（候选）胜率达标——即新主
// 质量回退——自动切回原主。
func (s *APIServer) maybeShadowRollback() {
	if s.deps.Shadow == nil {
		return
	}
	s.shadowMu.Lock()
	prev := s.shadowPrev
	s.shadowMu.Unlock()
	if prev == "" {
		return // 未处于金丝雀观察期
	}
	stats := s.deps.Shadow.Store.Stats("")
	judged := stats.Total - stats.Errors
	if judged < 10 {
		return // 样本不足，继续观察
	}
	// 候选此时为原主模型：候选胜率 ≥ 60% → 原主明显优于新主 → 回滚。
	oldWinRate := float64(stats.CandidateBetter) / float64(judged)
	if oldWinRate < 0.6 {
		return // 新主仍不劣于原主，继续观察
	}

	s.shadowMu.Lock()
	defer s.shadowMu.Unlock()
	if s.shadowPrev == "" {
		return // 已被并发回滚
	}
	if _, ok := s.deps.Router.Promote(prev); !ok {
		return
	}
	// 回滚后原主重新成为主模型，把影子候选重指向刚回滚的模型，
	// 恢复"候选 vs 主"的正常对比（避免回滚后又自我对比）。
	if next := s.deps.Router.Get(s.shadowPromoted); next != nil {
		s.deps.Shadow.SetCandidate(next)
	}
	s.shadowPrev = ""
	s.shadowPromoted = ""
	s.deps.Logger.Warn("金丝雀自动回滚：新主胜率不达标，已切回原主", "prev", prev)
	s.deps.Metrics.Inc("shadow:rollback")
	if s.deps.Audit != nil {
		s.deps.Audit.Log(safety.AuditEvent{
			Time: time.Now(), Action: "shadow.rollback", Target: prev,
			Risk: 2, Success: true,
		})
	}
}
