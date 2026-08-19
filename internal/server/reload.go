// 配置热更新（P18）：POST /v1/admin/reload —— 不重启服务重载技能/提示词/知识库。
//
// 生产价值：改 prompt、加技能、更新知识文档都不需要重新部署/重启，滚动生效。
// 本实现重载三个可热更新的组件：
//   - 技能（skills/ 目录）
//   - 提示词模板（prompts/ 目录）
//   - RAG 知识库（docs/ 目录）
//
// 由 main 注入的 Reload 闭包完成实际重载（server 层只负责鉴权与触发）。
package server

import (
	"encoding/json"
	"net/http"
)

// handleReload 触发配置热重载（仅 admin 角色）。
func (s *APIServer) handleReload(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if p.Role != "admin" { // RBAC：重载是高危管理操作
		http.Error(w, "仅 admin 可重载配置", http.StatusForbidden)
		return
	}
	if s.deps.Reload == nil {
		http.Error(w, "热更新未启用", http.StatusNotImplemented)
		return
	}
	if err := s.deps.Reload(); err != nil {
		s.writeAgentError(w, "热重载失败", err)
		return
	}
	s.deps.Logger.Info("配置已热重载", "user", p.User)
	jsonOK(w, map[string]string{"status": "reloaded"})
}

// jsonOK 写 JSON 响应（避免重复导入）。
func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.Encode(v)
}
