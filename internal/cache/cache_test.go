package cache

import (
	"context"
	"math"
	"testing"
)

// fakeEmbedder 确定性嵌入：1024 维、每字符计 1（去重）。
// 特征：相似字符串共享字符多 → 余弦高；完全不同字符 → 余弦≈0。
// 维度足够大，跨字符哈希碰撞可忽略，模拟真实嵌入器的语义区分度。
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, 1024)
	seen := make(map[int]bool)
	for _, r := range text {
		idx := int(r) % 1024
		if !seen[idx] {
			vec[idx] = 1
			seen[idx] = true
		}
	}
	return vec, nil
}

func TestSemanticHitAndMiss(t *testing.T) {
	// 注：测试用粗嵌入器相似度约 0.5~0.7，故阈值设 0.5；
	// 真实嵌入器（如 nomic-embed）对相似问题相似度可达 0.9+。
	c := New(fakeEmbedder{}, 0.5, 10)
	ctx := context.Background()

	c.Put(ctx, "北京天气怎么样", "北京今天 25 度，晴朗。")

	// 语义相近 → 命中
	if ans, ok := c.Get(ctx, "北京今天天气如何"); !ok {
		t.Fatal("相似问题应命中缓存")
	} else if ans != "北京今天 25 度，晴朗。" {
		t.Fatalf("命中内容错误: %q", ans)
	}

	// 完全无关 → 未命中
	if _, ok := c.Get(ctx, "帮我写一首诗"); ok {
		t.Fatal("无关问题不应命中")
	}
}

func TestCacheEviction(t *testing.T) {
	c := New(fakeEmbedder{}, 0.9, 2)
	ctx := context.Background()
	// 用字符差异极大的串，保证向量正交、不会因粗嵌入误命中
	c.Put(ctx, "aaaa", "a1")
	c.Put(ctx, "bbbb", "a2")
	c.Put(ctx, "cccc", "a3") // 淘汰 aaaa

	if _, ok := c.Get(ctx, "aaaa"); ok {
		t.Fatal("最旧条目应被淘汰")
	}
	if _, ok := c.Get(ctx, "cccc"); !ok {
		t.Fatal("最新条目应仍在缓存中")
	}
}

func TestCosine(t *testing.T) {
	if math.Abs(cosine([]float32{1, 0}, []float32{1, 0})-1) > 1e-6 {
		t.Fatal("相同向量余弦应≈1")
	}
	if math.Abs(cosine([]float32{1, 0}, []float32{0, 1})) > 1e-6 {
		t.Fatal("正交向量余弦应≈0")
	}
}
