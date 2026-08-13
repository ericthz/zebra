// 多模型路由（C15）：主备切换 / 降级 fallback / 按序尝试。
//
// 生产演进方向：
//   - 按任务类型路由（长文本→大模型、简单问答→小模型、成本优先→便宜模型）
//   - 健康度权重路由（结合 /metrics 的延迟与错误率打分）
//   - 配额感知路由（某模型额度耗尽自动切走）
//
// 本实现保持"顺序 fallback + 主备"骨架，替换策略不影响上层 Agent。
package provider

import (
	"context"
	"sync"
)

// Router 按顺序持有候选 Provider。
type Router struct {
	// chain 按优先级从高到低；index 指向主提供者（也即 chain 首个成功候选）。
	chain []Provider
	mu    sync.Mutex // 保护 chain（P42：影子评测异步 promote/回滚并发安全）
}

// NewRouter 构造。providers 第一个为默认主模型，其余为备选（fallback）。
func NewRouter(providers ...Provider) *Router {
	return &Router{chain: providers}
}

// Chain 返回完整候选链。
func (r *Router) Chain() []Provider { return r.chain }

// Primary 返回主模型（可能为 nil）。
func (r *Router) Primary() Provider {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.chain) == 0 {
		return nil
	}
	return r.chain[0]
}

// Promote 灰度切换（P26）：把指定名称的候选 Provider 提升为主模型。
// 返回（被顶替的原主模型名, 是否成功）；已位于主位时 prev 为空。
// 切换即时生效，后续请求走新主；prev 供金丝雀自动回滚（P42）使用。
func (r *Router) Promote(name string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := -1
	for i, p := range r.chain {
		if p != nil && p.Name() == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}
	if idx == 0 {
		return "", true // 候选已是主模型
	}
	prev := r.chain[0].Name()
	p := r.chain[idx]
	r.chain = append(r.chain[:idx], r.chain[idx+1:]...)
	r.chain = append([]Provider{p}, r.chain...)
	return prev, true
}

// ChatWithFallback 依次尝试每个候选，直到成功；返回命中的 Provider 供上层观测。
// 这同时实现了 B7 的"降级"：主模型故障 → 自动切备选，而不是把错误抛给用户。
func (r *Router) ChatWithFallback(ctx context.Context, messages []Message, tools []Tool) (Message, Provider, error) {
	var lastErr error
	for _, p := range r.chain {
		if p == nil {
			continue
		}
		msg, err := p.Chat(ctx, messages, tools)
		if err != nil {
			lastErr = err
			continue // 主模型失败 → 降级到下一个
		}
		return msg, p, nil
	}
	return Message{}, nil, lastErr
}
