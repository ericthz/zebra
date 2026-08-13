package rag

import (
	"context"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// rerankProvider 固定返回打分 JSON 的精排模型。
type rerankProvider struct{ reply string }

func (r *rerankProvider) Name() string { return "rerank" }
func (r *rerankProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: r.reply}, nil
}
func (r *rerankProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// flipReranker 测试桩：交换前两个候选，验证 RetrieveReranked 确实调用了重排。
type flipReranker struct{}

func (flipReranker) Rerank(_ context.Context, _ string, hits []Result) ([]Result, error) {
	if len(hits) >= 2 {
		hits[0], hits[1] = hits[1], hits[0]
	}
	return hits, nil
}

func TestLLMReranker(t *testing.T) {
	hits := []Result{{Chunk: Chunk{Text: "A"}}, {Chunk: Chunk{Text: "B"}}, {Chunk: Chunk{Text: "C"}}}
	r := &LLMReranker{Router: provider.NewRouter(&rerankProvider{
		reply: `{"scores":[{"index":2,"score":1.0},{"index":0,"score":0.5},{"index":1,"score":0.1}]}`,
	})}
	out, err := r.Rerank(context.Background(), "q", hits)
	if err != nil {
		t.Fatal(err)
	}
	// 应按分数重排为 C, A, B
	if out[0].Chunk.Text != "C" || out[1].Chunk.Text != "A" || out[2].Chunk.Text != "B" {
		t.Fatalf("精排顺序错误: %v %v %v", out[0].Chunk.Text, out[1].Chunk.Text, out[2].Chunk.Text)
	}
}

func TestLLMRerankerFallback(t *testing.T) {
	hits := []Result{{Chunk: Chunk{Text: "A"}}, {Chunk: Chunk{Text: "B"}}}
	r := &LLMReranker{Router: provider.NewRouter(&rerankProvider{reply: "not json"})}
	out, err := r.Rerank(context.Background(), "q", hits)
	if err != nil || out[0].Chunk.Text != "A" || out[1].Chunk.Text != "B" {
		t.Fatalf("评审失败应回退原顺序: %v %v", out, err)
	}
}

func TestRetrieveReranked(t *testing.T) {
	idx := NewIndex(fakeEmbedder{})
	ctx := context.Background()
	idx.AddDocument(ctx, "zebra 是 Go 写的企业级 AI Agent。", "zebra.md", 100, 0)
	idx.AddDocument(ctx, "QDRANT_URL 配置向量库。", "qdrant.md", 100, 0)
	idx.AddDocument(ctx, "北京的冬天很冷。", "beijing.md", 100, 0)

	hyb, err := idx.RetrieveHybrid(ctx, "北京的冬天", 2, 0.7)
	if err != nil || len(hyb) < 2 {
		t.Fatalf("混合检索应有至少 2 个候选: %v %v", hyb, err)
	}
	// 重排桩翻转前两个 → top1 应等于混合检索的第 2 名
	reranked, err := idx.RetrieveReranked(ctx, "北京的冬天", 1, flipReranker{})
	if err != nil || len(reranked) != 1 {
		t.Fatalf("重排检索异常: %v %v", reranked, err)
	}
	if reranked[0].Chunk.Source != hyb[1].Chunk.Source {
		t.Fatalf("重排后 top1 应为原第 2 名 %s，实际 %s", hyb[1].Chunk.Source, reranked[0].Chunk.Source)
	}
	// rr 为 nil → 退化为混合检索 top1
	plain, err := idx.RetrieveReranked(ctx, "北京的冬天", 1, nil)
	if err != nil || len(plain) != 1 || plain[0].Chunk.Source != hyb[0].Chunk.Source {
		t.Fatalf("nil 重排应退化为混合检索: %v %v", plain, err)
	}
}
