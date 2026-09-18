// 知识图谱 API：GET /v1/knowledge?entity=xxx
//
// 按实体反查三元组（作为主体或客体命中），返回 实体 → 关系列表。
// 知识图谱从 docs/ 抽取（规则抽取，见 internal/kg），供关系类问题检索。
package server

import "net/http"

// handleKnowledge 查询某实体相关的全部三元组。
func (s *APIServer) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	if _, ok := principal(r.Context()); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	entity := r.URL.Query().Get("entity")
	if entity == "" {
		http.Error(w, "bad request: entity 必填", http.StatusBadRequest)
		return
	}
	jsonOK(w, map[string]interface{}{
		"entity":  entity,
		"triples": s.deps.KG.Query(entity),
	})
}
