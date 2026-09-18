// RAG 重排：混合检索后再做一次"精排"，提升 topK 命中质量。
//
// 背景：向量/BM25 的初筛是"召回"，topK 里仍可能混入相关性一般的片段。
// 成熟 RAG 会加 Reranker 二次精排——本实现用 LLM 对候选片段逐条打
// 相关性分并重排（结构化输出约束），评审失败自动回退原顺序。
//
// 生产演化方向：独立小模型精排、按 query 类型切分精排策略、
// 精排分数与检索分数加权融合（此处简化为直接按精排分排序）。
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/provider"
)

// Reranker 对候选片段二次精排。
type Reranker interface {
	Rerank(ctx context.Context, query string, hits []Result) ([]Result, error)
}

// rerankSchema 精排输出 JSON Schema。
var rerankSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"scores": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{"type": "integer"},
					"score": map[string]interface{}{"type": "number"},
				},
				"required": []string{"index", "score"},
			},
		},
	},
	"required": []string{"scores"},
}

// LLMReranker LLM 精排器：让模型对候选片段逐条打相关性分（0~1）并重排。
type LLMReranker struct {
	Router  *provider.Router
	Timeout time.Duration
}

// Rerank 精排；任何失败（模型不可用/输出不合规）都回退原顺序，不劣化结果。
func (r *LLMReranker) Rerank(ctx context.Context, query string, hits []Result) ([]Result, error) {
	if len(hits) == 0 {
		return hits, nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()

	var b strings.Builder
	b.WriteString("你是检索重排器。请为每个候选片段与问题的【相关性】打分（0 到 1，越高越相关），只输出 JSON：\n")
	b.WriteString("{\"scores\":[{\"index\":0,\"score\":0.9},...]}\n问题：")
	b.WriteString(query)
	b.WriteString("\n候选片段：\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "[%d] %s\n", i, truncateRune(h.Chunk.Text, 300))
	}

	data, err := provider.StructuredChat(ctx, r.Router,
		[]provider.Message{{Role: "user", Content: b.String()}}, rerankSchema)
	if err != nil {
		return hits, nil // 评审不可用 → 回退原顺序
	}
	var out struct {
		Scores []struct {
			Index int     `json:"index"`
			Score float64 `json:"score"`
		} `json:"scores"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return hits, nil
	}

	// 默认分按原顺序递减（负数），保证"未评分候选"排在被评分之后
	scores := make([]float64, len(hits))
	for i := range scores {
		scores[i] = -float64(len(hits) - i)
	}
	for _, s := range out.Scores {
		if s.Index >= 0 && s.Index < len(hits) {
			scores[s.Index] = s.Score
		}
	}
	idx := make([]int, len(hits))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	reranked := make([]Result, len(hits))
	for i, j := range idx {
		reranked[i] = hits[j]
	}
	return reranked, nil
}

func (r *LLMReranker) timeout() time.Duration {
	if r.Timeout <= 0 {
		return 15 * time.Second
	}
	return r.Timeout
}

// RetrieveReranked 混合检索（候选取 topK*2）→ LLM 精排 → 截断 topK。
func (idx *Index) RetrieveReranked(ctx context.Context, query string, topK int, rr Reranker) ([]Result, error) {
	hits, err := idx.RetrieveHybrid(ctx, query, topK*2, 0.7)
	if err != nil {
		return nil, err
	}
	if rr != nil && len(hits) > 0 {
		if reranked, rerr := rr.Rerank(ctx, query, hits); rerr == nil {
			hits = reranked
		}
	}
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// truncateRune 按 rune 截断（避免切碎多字节中文）。
func truncateRune(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
