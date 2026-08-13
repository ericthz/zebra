// 技能检索：关键词打分 + 排序（当前最简实现，生产可换向量检索）。
package skill

import (
	"sort"
	"strings"
)

// rankSkills 按 query 对技能打分并降序返回 topN。
//
// 打分策略（启发式，够学习用）：
//   - 技能名命中一个词        → +3（名字最精确）
//   - 描述命中一个词          → +1（描述次之）
//   - 同一词重复出现按一次计    → 防长文本刷分
//
// 生产演化：替换为"query 向量 vs 技能描述向量"余弦相似度（复用 memory.Embedder），
// 打分接口不变。
func rankSkills(skills []*Skill, query string, limit int) []*Skill {
	tokens := keywordTokens(query)
	if len(tokens) == 0 {
		return nil
	}

	type scored struct {
		s     *Skill
		score int
	}
	var list []scored
	for _, s := range skills {
		score := 0
		seen := make(map[string]bool)
		for _, tok := range tokens {
			if seen[tok] {
				continue
			}
			seen[tok] = true
			if strings.Contains(strings.ToLower(s.Name), tok) {
				score += 3
			}
			if strings.Contains(strings.ToLower(s.Description), tok) {
				score += 1
			}
		}
		if score > 0 {
			list = append(list, scored{s: s, score: score})
		}
	}

	// 按分数降序
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].score > list[j].score
	})

	if limit <= 0 || limit > len(list) {
		limit = len(list)
	}
	out := make([]*Skill, 0, limit)
	for _, it := range list[:limit] {
		out = append(out, it.s)
	}
	return out
}
