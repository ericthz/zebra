// 遗忘机制深化：画像不是"只进不出"，要给记忆上"保质期"。
//
// 设计原则（借鉴 GDPR 数据最小化 + 产品层"记忆保鲜"）：
//   - TTL 过期：超过保鲜期未再提及的事实，不再注入上下文（读时惰性过滤）
//   - 物理清理：Sweep 把过期事实真正删掉（控制存储膨胀）
//   - 容量治理：单用户事实数封顶，超限丢最旧（防止画像被异常输入撑爆）
//
// 生产演化方向：按分类差异化 TTL（如 name 永久、preference 30 天）、
// 记忆合并（同义事实归一）、遗忘触发审计与用户可见的"我删了什么"。
package memory

import "time"

// ForgetPolicy 画像遗忘策略。
type ForgetPolicy struct {
	TTL             time.Duration          // 事实保鲜期；<=0 表示永不过期
	MaxFactsPerUser int                    // 单用户画像容量；<=0 表示不限制
	OnForget        func(user, key string) // 容量裁剪遗忘钩子（指标/审计，可为 nil）
}

// Apply 执行一轮遗忘：先物理清理过期事实，再做容量裁剪。
// 返回遗忘总条数（供指标/日志观测"记忆在正确衰减"）。
func (p *ForgetPolicy) Apply(store *ProfileStore, now time.Time) int {
	if store == nil {
		return 0
	}
	forgotten := store.Sweep(now, p.TTL)

	if p.MaxFactsPerUser > 0 {
		// 对每个用户做容量裁剪（Sweep 后可能仍超限，需逐个处理）
		for _, user := range store.Users() {
			dropped := store.TrimUser(user, p.MaxFactsPerUser)
			forgotten += dropped
			if p.OnForget != nil && dropped > 0 {
				p.OnForget(user, "capacity")
			}
		}
	}
	return forgotten
}

// Users 返回有画像的全部用户（供容量裁剪与运维统计）。
func (s *ProfileStore) Users() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.facts))
	for u := range s.facts {
		out = append(out, u)
	}
	return out
}
