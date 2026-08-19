// 工具注册表 + 权限边界（D20）。
//
// 三层防线：
//  1. 角色白名单 allow：role → 允许的工具集合（未配置的 role 视为 admin 全开）
//  2. 工具自身声明（Risky 接口）：高危需二次确认、指定角色
//  3. 审计钩子：所有调用落审计日志，高危操作额外记录参数
package tool

import (
	"context"
	"fmt"
	"sort"
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

// Remove 移除一个工具（P53 插件热重载用）。
func (r *Registry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		delete(r.tools, name)
		return true
	}
	return false
}

// Names 返回全部已注册工具名（排序，供启动清单/审计盘点使用）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// RiskLevel 返回指定工具的风险等级（未注册或非高危返回 0）。
// 供二次确认环节判断"该工具是否真的需要确认"，避免对普通工具误判。
func (r *Registry) RiskLevel(name string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return 0
	}
	if risky, isRisky := t.(Risky); isRisky {
		return risky.RiskLevel()
	}
	return 0
}

// AllowedRolesOf 返回工具自身声明的可用角色（未实现 Risky 接口返回空）。
func (r *Registry) AllowedRolesOf(name string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil
	}
	if risky, isRisky := t.(Risky); isRisky {
		return risky.AllowedRoles()
	}
	return nil
}

// Descriptions 返回 工具名 → 描述 的映射（供启动清单逐项展示工具说明）。
func (r *Registry) Descriptions() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.tools))
	for n, t := range r.tools {
		out[n] = t.Description()
	}
	return out
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

// Subset 返回只包含指定工具的子注册表（P13 多 Agent 专业化）：
// 未在 names 中的工具不复制；names 为空表示复制全部。
// 同时复制角色白名单 allow，保持权限语义一致；审计器 audit 一并复制
// （S-4：supervisor/worker 用 Subset 得到子注册表，若审计不复制则其工具
// 调用审计事件丢失，D20 审计链路断裂）。
func (r *Registry) Subset(names ...string) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := NewRegistry()
	allowSet := make(map[string]bool, len(names))
	for _, n := range names {
		allowSet[n] = true
	}
	for n, t := range r.tools {
		if len(names) == 0 || allowSet[n] {
			out.tools[n] = t
		}
	}
	for role, set := range r.allow {
		cp := make(map[string]bool, len(set))
		for k, v := range set {
			cp[k] = v
		}
		out.allow[role] = cp
	}
	out.audit = r.audit
	return out
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
