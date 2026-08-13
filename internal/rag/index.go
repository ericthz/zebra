// RAG 向量索引（检索增强生成，P8）。
//
// 价值：把"私有知识库"接进 Agent —— 用户问知识库里的问题，Agent 检索
// 相关片段作为上下文，回答就有据可依（接地/防幻觉），而不是模型瞎编。
//
// 管道（一条链，理解它 = 理解 RAG 全貌）：
//   摄取：文档 → ChunkText 分块 → Embed 嵌入 → 存入内存索引
//   检索：query → Embed → 余弦 topK → 返回 片段 + 来源 + 得分
//   注入：Agent 把命中的片段注入上下文（带来源标记，支持引用）
//
// 生产演化方向：
//   - 存储换 Qdrant/向量库（现有 memory.QdrantMemory 可直接复用做持久化）
//   - 混合检索（BM25 关键词 + 向量）+ 重排（rerank）提升精度
//   - 文档增量更新、去重、权限过滤（按租户隔离文档）
package rag

import (
	"context"
	"sort"
	"sync"

	"github.com/ericthz/zebra/internal/memory"
)

// Chunk 一个知识块（含来源与序号，供引用溯源）。
type Chunk struct {
	Text   string // 块内容
	Source string // 来源文档名/URL
	Seq    int    // 块序号（同一文档内从 1 起）
}

// Result 一次检索命中。
type Result struct {
	Chunk Chunk
	Score float64 // 余弦相似度
}

// Index 内存 RAG 索引（并发安全）。
type Index struct {
	embed memory.Embedder // 复用语义缓存的嵌入器（同一向量空间）
	mu    sync.RWMutex
	chunks []Chunk
	vecs   [][]float32
}

// NewIndex 构造空索引。
func NewIndex(embed memory.Embedder) *Index {
	return &Index{embed: embed}
}

// AddDocument 摄取一篇文档：分块 → 嵌入 → 追加到索引。
// size/overlap 传给分块器（0 则用默认）。
func (idx *Index) AddDocument(ctx context.Context, text, source string, size, overlap int) error {
	parts := ChunkText(text, size, overlap)
	if len(parts) == 0 {
		return nil
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	base := len(idx.chunks)
	for i, p := range parts {
		vec, err := idx.embed.Embed(ctx, p)
		if err != nil {
			return err
		}
		idx.chunks = append(idx.chunks, Chunk{Text: p, Source: source, Seq: base + i + 1})
		idx.vecs = append(idx.vecs, vec)
	}
	return nil
}

// Retrieve 检索与 query 最相关的 topK 个块（按相似度降序）。
func (idx *Index) Retrieve(ctx context.Context, query string, topK int) ([]Result, error) {
	qvec, err := idx.embed.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()
	type scored struct {
		r Result
	}
	all := make([]scored, 0, len(idx.chunks))
	for i, c := range idx.chunks {
		all = append(all, scored{r: Result{Chunk: c, Score: memory.Cosine(qvec, idx.vecs[i])}})
	}
	// 降序
	sort.Slice(all, func(i, j int) bool { return all[i].r.Score > all[j].r.Score })

	if topK <= 0 || topK > len(all) {
		topK = len(all)
	}
	out := make([]Result, 0, topK)
	for _, it := range all[:topK] {
		out = append(out, it.r)
	}
	return out, nil
}

// Len 索引中的块总数（用于统计/监控）。
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.chunks)
}
