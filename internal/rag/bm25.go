// BM25 关键词检索（P20 RAG 混合检索）。
//
// 背景：纯向量检索依赖嵌入质量，对"专有名词/编号/精确术语"容易丢分
// （如搜"QDRANT_URL"或"HTTP_TIMEOUT"，向量未必命中，但关键词必然命中）。
// 成熟 RAG 采用【混合检索】：BM25 关键词 + 向量 各自打分再融合，扬长避短。
//
// BM25 公式（经典 Robertson 版）：
//
//	score(q,d) = Σ IDF(t) * f(t,d)*(k1+1) / (f(t,d) + k1*(1-b+b*|d|/avgdl))
//
// 其中 f=词频，|d|/avgdl=文档长度归一，k1≈1.5, b≈0.75。
package rag

import (
	"math"
	"strings"
)

// bm25 一次检索的 BM25 打分器（基于当前语料统计）。
type bm25 struct {
	docFreq  map[string]int   // term -> 出现该词的文档数
	docTerms []map[string]int // 每块内的词频表（BM25 需要 f(t,d)）
	docLen   []int            // 每块长度（token 数）
	avgLen   float64          // 平均块长度
	k1       float64
	b        float64
}

// newBM25 基于分块语料构建统计。
func newBM25(chunks []Chunk) *bm25 {
	b := &bm25{docFreq: make(map[string]int), k1: 1.5, b: 0.75}
	b.docLen = make([]int, len(chunks))
	b.docTerms = make([]map[string]int, len(chunks))
	var total int
	for i, c := range chunks {
		terms := tokenize(c.Text)
		b.docLen[i] = len(terms)
		total += len(terms)
		// 文档内词频表：BM25 的核心是 f(t,d)——词在"这篇文档"里出现几次
		// （不能用 query 词频，否则不包含查询词的文档也会凭空得分）。
		b.docTerms[i] = make(map[string]int)
		seen := make(map[string]bool)
		for _, t := range terms {
			b.docTerms[i][t]++
			if !seen[t] {
				seen[t] = true
				b.docFreq[t]++
			}
		}
	}
	if len(chunks) > 0 {
		b.avgLen = float64(total) / float64(len(chunks))
	}
	return b
}

// idf 逆文档频率（平滑，避免除零）。
func (b *bm25) idf(term string) float64 {
	n := float64(b.docFreq[term])
	N := float64(len(b.docLen))
	return math.Log(1 + (N-n+0.5)/(n+0.5))
}

// score 计算 query 对第 idx 块的 BM25 得分。
func (b *bm25) score(query string, idx int) float64 {
	var s float64
	for _, t := range tokenize(query) {
		f := float64(b.docTerms[idx][t]) // f(t,d)：该词在文档中的出现次数
		if f == 0 {
			continue // 文档里没有这个词 → 该项不贡献
		}
		denom := f + b.k1*(1-b.b+b.b*float64(b.docLen[idx])/b.avgLen)
		if denom == 0 {
			continue
		}
		s += b.idf(t) * f * (b.k1 + 1) / denom
	}
	return s
}

// tokenize 中文逐字 + 英文整词切分（与技能/路由检索同策略）。
func tokenize(s string) []string {
	var out []string
	for _, field := range strings.Fields(strings.ToLower(s)) {
		ascii := true
		for _, r := range field {
			if r >= 0x4e00 && r <= 0x9fff {
				out = append(out, string(r))
				ascii = false
			}
		}
		if ascii {
			out = append(out, field)
		}
	}
	return out
}
