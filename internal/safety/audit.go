// D20 审计日志：所有敏感/高危操作留痕，支撑合规追溯。
package safety

import (
	"log/slog"
	"time"
)

// AuditEvent 审计事件。
type AuditEvent struct {
	Time    time.Time `json:"time"`
	User    string    `json:"user"`
	Role    string    `json:"role"`
	Action  string    `json:"action"` // 例如 tool.call / auth.fail / confirm
	Target  string    `json:"target"` // 例如工具名/资源
	Detail  string    `json:"detail"` // 脱敏后的参数/信息
	Risk    int       `json:"risk"`   // 0 常规 / 1 关注 / 2 高危
	Success bool      `json:"success"`
}

// AuditLog 审计通道。
type AuditLog interface {
	Log(evt AuditEvent)
}

// StdAuditLog 基于结构化日志的审计实现。
type StdAuditLog struct {
	logger *slog.Logger
}

// NewStdAuditLog 构造。
func NewStdAuditLog(logger *slog.Logger) *StdAuditLog {
	return &StdAuditLog{logger: logger}
}

// Log 落审计日志（生产可接 Kafka/对象存储做长期留档）。
func (a *StdAuditLog) Log(evt AuditEvent) {
	a.logger.Info("audit",
		"action", evt.Action, "user", evt.User, "role", evt.Role,
		"target", evt.Target, "risk", evt.Risk, "success", evt.Success,
		"detail", Redact(evt.Detail), // D19：落库前强制脱敏
		"time", evt.Time.Format(time.RFC3339),
	)
}
