// 工具注册表 + 权限边界（D20）。
//
// 三层防线：
//   1. 角色白名单 allow：role → 允许的工具集合（未配置的 role 视为 admin 全开）
//   2. 工具自身声明（Risky 接口）：高危需二次确认、指定角色
//   3. 审计钩子：所有调用落审计日志，高危操作额外记录参数
package tool

import (
	"context"
	"fmt"
	"sync"

	"github.com/ericthz/zebra/internal/provider"
)

// Auditor 审计接口（由 server 层注入真实实现，避免依赖倒置）。
type Auditor interface {
	LogToolCall(user, role, toolName string, risk int, args map[string]interface{}, result string, err error)
}

// Registry 工具注册表（并发安全）。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	// allow: role → 允许的工具名集合；role 不在表中视为无限制。
	allow map[string]map[string]bool
	audit Auditor
}

// NewRegistry 构造。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool), allow: make(map[string]map[string]bool)}
}

// SetAuditor 注入审计实现。
func (r *Registry) SetAuditor(a Auditor) { r.audit = a }

// Register 注册工具。
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// AllowTool 授予某角色使用某工具的权限。默认仅 admin 全开。
func (r *Registry) AllowTool(role, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.allow[role] == nil {
		r.allow[role] = make(map[string]bool)
	}
	r.allow[role][name] = true
}

// DenyTool 收回某角色的工具权限。
// 注意：DenyTool 会同时把该角色"登记"为已配置角色，使其从默认全开
// 变为白名单模式（此后该角色仅能用 AllowTool 显式授予的工具）。
func (r *Registry) DenyTool(role, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	set, ok := r.allow[role]
	if !ok {
		set = make(map[string]bool)
		r.allow[role] = set
	}
	delete(set, name)
}

// ToolsFor 返回某角色可见的工具列表（转成 provider.Tool 供模型消费）。
func (r *Registry) ToolsFor(role string) []provider.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []provider.Tool
	for _, t := range r.tools {
		if !r.allowed(role, t) {
			continue
		}
		out = append(out, provider.Tool{
			Type: "function",
			Function: provider.FunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return out
}

// Execute 安全执行工具。confirm 用于高危工具的人工二次确认（D20）。
func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}, user, role string, confirm bool) (string, error) {
	r.mu.RLock()
	t, ok := r.tools[name]
	allowed := ok && r.allowed(role, t)
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("未找到工具: %s", name)
	}
	if !allowed {
		return "", fmt.Errorf("角色 %s 无权限调用工具 %s", role, name)
	}

	risk := 0
	if risky, isRisky := t.(Risky); isRisky {
		risk = risky.RiskLevel()
		if risk >= 2 && !confirm {
			return "", fmt.Errorf("工具 %s 为高危操作，需要二次确认", name)
		}
	}

	if err := ValidateArgs(t, args); err != nil { // C13 结构化输出校验
		return "", err
	}

	result, err := t.Execute(ctx, args)
	if r.audit != nil {
		r.audit.LogToolCall(user, role, name, risk, args, result, err)
	}
	return result, err
}

func (r *Registry) allowed(role string, t Tool) bool {
	// 工具自身声明角色限制
	if risky, isRisky := t.(Risky); isRisky {
		roles := risky.AllowedRoles()
		if len(roles) > 0 {
			found := false
			for _, rl := range roles {
				if rl == role {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	// 角色白名单：admin 或未配置的 role 不受限
	if role == "admin" {
		return true
	}
	set, configured := r.allow[role]
	if !configured {
		return true
	}
	return set[t.Name()]
}
