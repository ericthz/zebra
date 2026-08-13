// tool.Auditor → safety.AuditLog 适配：工具调用审计接入统一审计通道（D20）。
package server

import (
	"encoding/json"
	"time"

	"github.com/ericthz/zebra/internal/safety"
)

// auditAdapter 实现 tool.Auditor 接口。
type auditAdapter struct {
	log safety.AuditLog
}

// NewToolAuditor 构造工具审计适配器（供装配层注入）。
func NewToolAuditor(log safety.AuditLog) *auditAdapter {
	return &auditAdapter{log: log}
}

// LogToolCall 记录一次工具调用，参数落库前脱敏（D19）。
func (a *auditAdapter) LogToolCall(user, role, toolName string, risk int, args map[string]interface{}, result string, err error) {
	detail, _ := json.Marshal(args)
	evt := safety.AuditEvent{
		Time: time.Now(), User: user, Role: role,
		Action: "tool.call", Target: toolName,
		Detail:  safety.Redact(string(detail)),
		Risk:    risk,
		Success: err == nil,
	}
	a.log.Log(evt)
}
