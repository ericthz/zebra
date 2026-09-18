// tool.Auditor → safety.AuditLog 适配：工具调用审计 + 成功率指标（质量闭环）。
package server

import (
	"time"

	"github.com/ericthz/zebra/internal/safety"
)

// auditAdapter 实现 tool.Auditor 接口。
type auditAdapter struct {
	log     safety.AuditLog
	metrics *Metrics // 记录工具成功率指标（挂 /metrics）
}

// NewToolAuditor 构造工具审计适配器（供装配层注入）。
// metrics 可为 nil（不记录指标，仅审计）。
func NewToolAuditor(log safety.AuditLog, metrics *Metrics) *auditAdapter {
	return &auditAdapter{log: log, metrics: metrics}
}

// LogToolCall 记录一次工具调用：
//   - 审计事件（参数落库前脱敏：复用 RedactArgs 白名单，与 OnTool
//     日志同一强度——run_command 完整命令串、write_file content 等可携带
//     机密的键绝不原样落审计日志）
//   - 成功率指标：tool_call:<name>:ok / tool_call:<name>:fail
func (a *auditAdapter) LogToolCall(user, role, toolName string, risk int, args map[string]interface{}, result string, err error) {
	detail := safety.RedactArgs(args)
	evt := safety.AuditEvent{
		Time: time.Now(), User: user, Role: role,
		Action: "tool.call", Target: toolName,
		Detail:  detail,
		Risk:    risk,
		Success: err == nil,
	}
	if a.log != nil {
		a.log.Log(evt)
	}
	if a.metrics != nil {
		// 成功率 = ok / (ok+fail)，可在 /metrics 观察并配置告警
		name := "tool_call:" + toolName
		if err == nil {
			a.metrics.Inc(name + ":ok")
		} else {
			a.metrics.Inc(name + ":fail")
		}
	}
}
