// 被遗忘权（P6/GDPR）：DELETE /v1/user/data —— 删除该用户全链路数据。
//
// 触发范围（循序渐进，先做最有价值的三处）：
//  1. 会话：删除该用户全部会话（含历史）
//  2. 工作记忆：清空这些会话的进程内记忆
//  3. 长期记忆：清空该租户的向量集合（Qdrant）
//  4. 用户画像：删除该用户全部画像事实（P22）
//
// 生产演化：还需清理日志/审计/缓存/账单中的个人数据，并落"删除请求"审计。
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/ericthz/zebra/internal/safety"
)

// handleForget 删除当前认证用户的全部数据。
func (s *APIServer) handleForget(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 1. 删除会话
	var sessionIDs []string
	sessionIDs = s.deps.Sessions.ForgetUser(p.User)

	// 2. 清空工作记忆
	if s.deps.Mem != nil {
		for _, id := range sessionIDs {
			s.deps.Mem.ForgetSession(id)
		}
	}

	// 3. 清空该租户长期记忆（向量集合）
	if s.deps.Mem != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.deps.Mem.ForgetTenant(ctx, p.Tenant); err != nil {
			s.deps.Logger.Warn("忘记租户长期记忆失败", "tenant", p.Tenant, "err", err)
		}
	}

	// 4. 删除用户画像（P22 扩展被遗忘权覆盖范围）
	if s.deps.Profile != nil {
		s.deps.Profile.ForgetUser(p.User)
	}

	// 5. 审计删除请求（删除操作本身必须留痕，供合规追溯）
	if s.deps.Audit != nil {
		s.deps.Audit.Log(safety.AuditEvent{
			Time: time.Now(), User: p.User, Role: p.Role,
			Action: "data.forget", Target: "user:" + p.User,
			Detail: "sessions=" + itoa(len(sessionIDs)), Risk: 1, Success: true,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}
