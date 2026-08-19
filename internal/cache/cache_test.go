package cache

import (
	"context"
	"math"
	"testing"

	"github.com/ericthz/zebra/internal/memory"
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

	c.Put(ctx, "user-a", "北京天气怎么样", "北京今天 25 度，晴朗。")

	// 语义相近 → 命中（同 scope 内）
	if ans, ok := c.Get(ctx, "user-a", "北京今天天气如何"); !ok {
		t.Fatal("相似问题应命中缓存")
	} else if ans != "北京今天 25 度，晴朗。" {
		t.Fatalf("命中内容错误: %q", ans)
	}

	// 完全无关 → 未命中
	if _, ok := c.Get(ctx, "user-a", "帮我写一首诗"); ok {
		t.Fatal("无关问题不应命中")
	}
}

func TestCacheTenantIsolation(t *testing.T) {
	c := New(fakeEmbedder{}, 0.5, 10)
	ctx := context.Background()

	// A 用户的问题写入缓存后，B 用户即使问完全一样的问题也不得命中（A4 隔离）
	c.Put(ctx, "user-a", "我叫什么名字", "你叫小明。")

	if _, ok := c.Get(ctx, "user-b", "我叫什么名字"); ok {
		t.Fatal("跨用户（租户）不应命中缓存")
	}
	if ans, ok := c.Get(ctx, "user-a", "我叫什么名字"); !ok || ans != "你叫小明。" {
		t.Fatalf("同用户应命中: ok=%v ans=%q", ok, ans)
	}
}

func TestCacheEviction(t *testing.T) {
	c := New(fakeEmbedder{}, 0.9, 2)
	ctx := context.Background()
	// 用字符差异极大的串，保证向量正交、不会因粗嵌入误命中
	c.Put(ctx, "user-a", "aaaa", "a1")
	c.Put(ctx, "user-a", "bbbb", "a2")
	c.Put(ctx, "user-a", "cccc", "a3") // 淘汰 aaaa

	if _, ok := c.Get(ctx, "user-a", "aaaa"); ok {
		t.Fatal("最旧条目应被淘汰")
	}
	if _, ok := c.Get(ctx, "user-a", "cccc"); !ok {
		t.Fatal("最新条目应仍在缓存中")
	}
}

// TestCacheZeroEntriesNoPanic 六12：maxEntries<=0 时不得越界 panic，
// 也不得无限增长（New 强制下限 1）。
func TestCacheZeroEntriesNoPanic(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int{0, -1, -100} {
		c := New(fakeEmbedder{}, 0.9, n)
		for i := 0; i < 10; i++ {
			c.Put(ctx, "user-a", "q"+string(rune('a'+i)), "ans") // 不得 panic
		}
		if len(c.items) > 1 {
			t.Fatalf("maxEntries=%d 时缓存应被钳制到 1 条，实际 %d", n, len(c.items))
		}
	}
}

func TestCosine(t *testing.T) {
	if math.Abs(memory.Cosine([]float32{1, 0}, []float32{1, 0})-1) > 1e-6 {
		t.Fatal("相同向量余弦应≈1")
	}
	if math.Abs(memory.Cosine([]float32{1, 0}, []float32{0, 1})) > 1e-6 {
		t.Fatal("正交向量余弦应≈0")
	}
}
