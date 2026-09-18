// 记忆画像：从对话里持续提炼"关于用户的事实"，形成可复用的用户画像。
//
// 背景：记忆系统只会"存对话"，但产品要的是"懂用户"——下次对话自动带上
// 已知偏好/事实，少问一遍。本文件实现：
//   - Fact：一条画像事实（键/值/分类/置信度/最近出现时间）
//   - RuleProfileExtractor：规则抽取器（纯正则，确定性、可测试、零模型成本）
//   - ProfileStore：按用户隔离的画像存储（并发安全）
//
// 生产演化方向：规则抽取器可替换为 LLM 抽取（接口已留出）；画像落到
// DB/Redis，配合遗忘策略（forget.go）做 TTL 衰减与合并。
package memory

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// Fact 一条用户画像事实。
type Fact struct {
	Key        string    `json:"key"`        // 事实键（name/preference/location/...）
	Value      string    `json:"value"`      // 事实值（"小明"/"火锅"/"北京"）
	Category   string    `json:"category"`   // 分类（identity/preference/lifestyle/...）
	Confidence float64   `json:"confidence"` // 置信度 0~1（显式陈述高，推测低）
	LastSeen   time.Time `json:"last_seen"`  // 最近一次出现时间（遗忘机制依据）
}

// ProfileStore 按用户存储画像事实（并发安全；生产换 Redis/DB + 序列化）。
type ProfileStore struct {
	mu        sync.Mutex
	facts     map[string]map[string]Fact // user → key → fact
	conflicts map[string][]Conflict      // user → 冲突历史
}

// Conflict 一次画像事实冲突记录：同一 key 出现不同取值（且置信度可比）。
type Conflict struct {
	Key      string    `json:"key"`
	OldValue string    `json:"old_value"`
	NewValue string    `json:"new_value"`
	Time     time.Time `json:"time"`
}

// NewProfileStore 构造空画像存储。
func NewProfileStore() *ProfileStore {
	return &ProfileStore{
		facts:     make(map[string]map[string]Fact),
		conflicts: make(map[string][]Conflict),
	}
}

// Learn 把抽取到的事实合并进某用户画像。
// 同 key 取"置信度更高或时间更新"的版本（合并去重，不无限膨胀）。
func (s *ProfileStore) Learn(user string, facts []Fact, now time.Time) {
	if len(facts) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	um := s.facts[user]
	if um == nil {
		um = make(map[string]Fact)
		s.facts[user] = um
	}
	for _, f := range facts {
		f.LastSeen = now
		if old, ok := um[f.Key]; ok {
			// 合并策略：置信度优先，同置信度取更新者（低置信度新值不覆盖高置信度旧值）
			if old.Confidence > f.Confidence {
				continue
			}
			if old.Confidence == f.Confidence && old.LastSeen.After(f.LastSeen) {
				continue
			}
			// 冲突消解：不同取值且新置信度与旧值可比 → 记录冲突，不静默覆盖
			if old.Value != f.Value && f.Confidence >= old.Confidence*0.8 {
				s.conflicts[user] = append(s.conflicts[user], Conflict{
					Key: f.Key, OldValue: old.Value, NewValue: f.Value, Time: now,
				})
			}
		}
		um[f.Key] = f
	}
}

// ConflictsFor 返回某用户的画像冲突历史（新在前）。
func (s *ProfileStore) ConflictsFor(user string) []Conflict {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.conflicts[user]
	out := make([]Conflict, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i])
	}
	return out
}

// ResolveConflict 裁决某 key 的最新冲突：
//
//	keepOld=true  → 回退为旧值（并刷新时间）
//	keepOld=false → 保留新值
//
// 无论哪种都会清除该冲突记录；不存在冲突返回 false。
func (s *ProfileStore) ResolveConflict(user, key string, keepOld bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.conflicts[user]
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Key != key {
			continue
		}
		c := list[i]
		s.conflicts[user] = append(list[:i], list[i+1:]...)
		if keepOld {
			if um, ok := s.facts[user]; ok {
				if f, ok := um[key]; ok {
					f.Value = c.OldValue
					f.LastSeen = time.Now()
					um[key] = f
				}
			}
		}
		return true
	}
	return false
}

// Consolidate 画像合并：同一分类下"取值归一化后相同"的事实只保留置信度
// 最高的一条（如 preference：火锅 与 吃火锅 合并），返回移除条数。
func (s *ProfileStore) Consolidate(user string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	um := s.facts[user]
	keep := map[string]string{} // 组 key（分类+归一化值）→ 保留的事实 key
	drop := map[string]bool{}
	for k, f := range um {
		gk := f.Category + "\x00" + normalizeValue(f.Value)
		if cur, ok := keep[gk]; ok {
			if um[cur].Confidence >= f.Confidence {
				drop[k] = true
			} else {
				drop[cur] = true
				keep[gk] = k
			}
		} else {
			keep[gk] = k
		}
	}
	n := 0
	for k := range drop {
		delete(um, k)
		n++
	}
	return n
}

// normalizeValue 偏好值归一化：去掉"吃/喝/用/喜欢"等动词前缀，便于合并。
func normalizeValue(v string) string {
	for _, p := range []string{"吃", "喝", "用", "喜欢"} {
		if strings.HasPrefix(v, p) {
			return strings.TrimSpace(v[len(p):])
		}
	}
	return v
}

// FactsFor 返回某用户未过期的画像事实（TTL 过滤，读时惰性遗忘）。
// ttl<=0 表示不过期。返回按 LastSeen 降序。
func (s *ProfileStore) FactsFor(user string, now time.Time, ttl time.Duration) []Fact {
	s.mu.Lock()
	defer s.mu.Unlock()
	um := s.facts[user]
	if um == nil {
		return nil
	}
	out := make([]Fact, 0, len(um))
	for _, f := range um {
		if ttl > 0 && now.Sub(f.LastSeen) > ttl {
			continue // 超 TTL 的事实不参与注入（但仍保留，等 Sweep 物理清理）
		}
		out = append(out, f)
	}
	sortByLastSeenDesc(out)
	return out
}

// ForgetKey 删除某用户的一条画像事实。
func (s *ProfileStore) ForgetKey(user, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if um, ok := s.facts[user]; ok {
		if _, has := um[key]; has {
			delete(um, key)
			return true
		}
	}
	return false
}

// ForgetUser 删除某用户的全部画像（被遗忘权， 扩展）。
func (s *ProfileStore) ForgetUser(user string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.facts[user])
	delete(s.facts, user)
	return n
}

// Sweep 物理清理所有用户超 TTL 的事实（维护任务；返回清理条数）。
func (s *ProfileStore) Sweep(now time.Time, ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for user, um := range s.facts {
		for key, f := range um {
			if now.Sub(f.LastSeen) > ttl {
				delete(um, key)
				n++
			}
		}
		if len(um) == 0 {
			delete(s.facts, user)
		}
	}
	return n
}

// Count 返回某用户画像事实数（0 表示无画像）。
func (s *ProfileStore) Count(user string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.facts[user])
}

// TrimUser 画像容量治理：某用户事实数超过 max 时，丢弃最旧的事实。
// 返回丢弃条数（防止画像无限膨胀，配合遗忘策略使用）。
func (s *ProfileStore) TrimUser(user string, max int) int {
	if max <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	um := s.facts[user]
	if len(um) <= max {
		return 0
	}
	all := make([]Fact, 0, len(um))
	for _, f := range um {
		all = append(all, f)
	}
	sortByLastSeenDesc(all)
	dropped := 0
	for _, f := range all[max:] {
		delete(um, f.Key)
		dropped++
	}
	return dropped
}

func sortByLastSeenDesc(f []Fact) {
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && f[j].LastSeen.After(f[j-1].LastSeen); j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

// ---- 规则抽取器（确定性、零成本；生产可替换为 LLM 抽取）----

// 常见中文陈述模式（按置信度区分：显式自我介绍 > 偏好陈述）。
var factRules = []struct {
	key        string
	category   string
	confidence float64
	re         *regexp.Regexp
}{
	{key: "name", category: "identity", confidence: 0.95, re: regexp.MustCompile(`(?:我叫|我的名字是|名字叫)([\p{Han}A-Za-z0-9]{1,12})`)},
	{key: "preference", category: "preference", confidence: 0.85, re: regexp.MustCompile(`我(?:喜欢|爱吃|最爱)(?:吃|喝|用)?([\p{Han}A-Za-z0-9]{1,20})`)},
	{key: "hobby", category: "preference", confidence: 0.8, re: regexp.MustCompile(`(?:我的爱好是|我喜欢(?:的)?(?:爱好|运动|活动)是)([\p{Han}A-Za-z0-9]{1,20})`)},
	{key: "location", category: "lifestyle", confidence: 0.8, re: regexp.MustCompile(`我住在([\p{Han}A-Za-z0-9]{1,16})`)},
	{key: "work", category: "lifestyle", confidence: 0.8, re: regexp.MustCompile(`我在([\p{Han}A-Za-z0-9]{1,16})(?:公司|单位|工作)`)},
	{key: "age", category: "identity", confidence: 0.85, re: regexp.MustCompile(`(?:我(?:今年|现在)?|今年)\s*(\d{1,3})\s*岁`)},
	{key: "birthday", category: "identity", confidence: 0.9, re: regexp.MustCompile(`我的生日是([\d年月日\-/]{4,12})`)},
}

// ExtractFacts 从一句话中抽取全部画像事实（多模式命中取置信度最高者）。
func ExtractFacts(text string) []Fact {
	var out []Fact
	for _, r := range factRules {
		m := r.re.FindStringSubmatch(text)
		if len(m) < 2 {
			continue
		}
		val := strings.TrimSpace(m[1])
		if val == "" {
			continue
		}
		out = append(out, Fact{
			Key: r.key, Value: val, Category: r.category,
			Confidence: r.confidence,
		})
	}
	return out
}
