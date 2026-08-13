// Package cache 语义缓存（P5 成本治理）。
//
// 价值：用户反复问相似问题（如"今天天气""今天天气怎么样"）时，
// 若语义相近，直接返回缓存答案，省一次 LLM 调用（省钱 + 省延迟）。
// 这是比 prompt cache 更进一步的"业务级缓存"。
//
// 实现：
//   - 用嵌入向量计算"新问题 vs 缓存问题"的余弦相似度
//   - 超过阈值 → 命中（hit），否则未命中
//   - 简单 FIFO 淘汰（容量上限），并发安全，统计命中率
//
// 生产演化方向：
//   - 缓存命中后仍可异步刷新（防结果过时）
//   - 存储落 Redis（多实例共享缓存）
//   - 仅缓存"无副作用"的纯文本回答（本包由调用方保证）
package cache

import (
	"context"
	"sync"

	"github.com/ericthz/zebra/internal/memory"
)

// SemanticCache 语义缓存。
type SemanticCache struct {
	embed      memory.Embedder
	threshold  float64 // 相似度阈值 [0,1]，越大越严格
	maxEntries int

	mu     sync.Mutex
	items  []cacheItem // FIFO 队列，达到上限淘汰最旧
	hits   int64
	misses int64
}

type cacheItem struct {
	query  string
	answer string
	vec    []float32
}

// New 构造语义缓存。
// threshold 建议 0.90~0.95（过高难命中，过低易误命中）。
func New(embed memory.Embedder, threshold float64, maxEntries int) *SemanticCache {
	return &SemanticCache{embed: embed, threshold: threshold, maxEntries: maxEntries}
}

// Get 检索缓存；命中返回 (answer, true)。
func (c *SemanticCache) Get(ctx context.Context, query string) (string, bool) {
	vec, err := c.embed.Embed(ctx, query)
	if err != nil {
		c.miss()
		return "", false // 嵌入失败按未命中处理（不阻塞业务）
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	best := -1.0
	var bestAnswer string
	for _, it := range c.items {
		if sim := memory.Cosine(vec, it.vec); sim > best {
			best = sim
			bestAnswer = it.answer
		}
	}
	if best >= c.threshold {
		c.hits++
		return bestAnswer, true
	}
	c.misses++
	return "", false
}

// Put 写入一条缓存（达到上限淘汰最旧）。
func (c *SemanticCache) Put(ctx context.Context, query, answer string) {
	vec, err := c.embed.Embed(ctx, query)
	if err != nil || answer == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, cacheItem{query: query, answer: answer, vec: vec})
	if len(c.items) > c.maxEntries {
		c.items = c.items[len(c.items)-c.maxEntries:]
	}
}

// Stats 命中/未命中计数（用于命中率指标）。
func (c *SemanticCache) Stats() (hits, misses int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

func (c *SemanticCache) miss() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses++
}
