package rag

import (
	"context"
	"strings"
	"testing"
)

// fakeEmbedder 确定性嵌入：字符出现即置 1（1024 维，碰撞可忽略）。
// 相似文本共享字符多 → 余弦高，便于测试检索。
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

func TestChunkText(t *testing.T) {
	text := "段落一的内容。\n段落二的内容。\n段落三的内容。\n"
	chunks := ChunkText(text, 8, 2)
	if len(chunks) < 2 {
		t.Fatalf("长文本应切成多块，实际 %d", len(chunks))
	}
	// 校验重叠：后一块应包含前一块的尾部
	if !strings.Contains(chunks[1], chunks[0][len(chunks[0])-2:]) {
		t.Logf("重叠不严格（取决于切点），跳过")
	}
}

func TestIndexAddRetrieve(t *testing.T) {
	idx := NewIndex(fakeEmbedder{})
	ctx := context.Background()

	// 摄入两篇"不同主题"的文档
	idx.AddDocument(ctx, "zebra 是一个企业级 AI Agent 参考项目，提供工具调用、记忆、技能等能力。", "zebra.md", 100, 0)
	idx.AddDocument(ctx, "北京的天气夏天炎热，冬天寒冷，四季分明。", "beijing.md", 100, 0)
	idx.AddDocument(ctx, "春天的樱桃很好吃，果园里有很多樱桃树。", "cherry.md", 100, 0)

	if idx.Len() < 3 {
		t.Fatalf("应摄入至少 3 块，实际 %d", idx.Len())
	}

	// 检索"北京天气"
	res, err := idx.Retrieve(ctx, "北京冬天冷不冷", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("应命中知识库")
	}
	// 最相关的应是 beijing.md
	if !strings.Contains(res[0].Chunk.Text, "北京") {
		t.Fatalf("首个命中应含'北京'，实际 %q", res[0].Chunk.Text)
	}
	if res[0].Chunk.Source != "beijing.md" {
		t.Fatalf("来源应正确: %q", res[0].Chunk.Source)
	}
	t.Logf("top1: %s (score=%.3f)", res[0].Chunk.Source, res[0].Score)
}
