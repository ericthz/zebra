package rag

import (
	"context"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	// 中文逐字 + 英文整词
	got := tokenize("QDRANT_URL 服务 北京 HTTP_TIMEOUT")
	want := []string{"qdrant_url", "服", "务", "北", "京", "http_timeout"}
	if len(got) != len(want) {
		t.Fatalf("token 数量不匹配: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token[%d] = %q, want %q (全部: %v)", i, got[i], want[i], got)
		}
	}
}

func TestBM25Score(t *testing.T) {
	chunks := []Chunk{
		{Text: "QDRANT_URL 是长期记忆的向量库地址，配置后启用。"},
		{Text: "北京的冬天很冷，需要穿羽绒服。"},
		{Text: "语义缓存命中后响应速度大幅提升。"},
	}
	b := newBM25(chunks)
	// 精确术语"QDRANT_URL"应对第 0 块得分最高
	best := 0
	bestScore := b.score("QDRANT_URL 是什么", 0)
	for i := 1; i < len(chunks); i++ {
		if s := b.score("QDRANT_URL 是什么", i); s > bestScore {
			best, bestScore = i, s
		}
	}
	if best != 0 {
		t.Fatalf("BM25 应命中第 0 块，实际第 %d 块 (score=%f)", best, bestScore)
	}
}

func TestRetrieveHybrid(t *testing.T) {
	idx := NewIndex(fakeEmbedder{})
	ctx := context.Background()

	// 摄入"语义相近但无精确术语"的两篇 + 一篇含专有名词
	idx.AddDocument(ctx, "zebra 提供企业级 AI Agent 能力，包括记忆、技能、工具调用。", "zebra.md", 100, 0)
	idx.AddDocument(ctx, "QDRANT_URL 配置后启用长期记忆，连接向量数据库。", "qdrant.md", 100, 0)
	idx.AddDocument(ctx, "北京的冬天很冷，夏天炎热，四季分明。", "beijing.md", 100, 0)

	// 纯向量检索：语义命中 zebra.md
	vec, err := idx.Retrieve(ctx, "智能体平台能力", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) == 0 || !strings.Contains(vec[0].Chunk.Text, "zebra") {
		t.Fatalf("纯向量应命中 zebra.md，实际 %+v", vec)
	}

	// 混合检索：精确术语命中 qdrant.md（向量对专有名词易失分，BM25 补上）
	hyb, err := idx.RetrieveHybrid(ctx, "QDRANT_URL 怎么配", 1, 0.7)
	if err != nil {
		t.Fatal(err)
	}
	if len(hyb) == 0 || !strings.Contains(hyb[0].Chunk.Text, "QDRANT_URL") {
		t.Fatalf("混合检索应命中 qdrant.md，实际 %+v", hyb)
	}

	// 空索引安全
	empty := NewIndex(fakeEmbedder{})
	got, err := empty.RetrieveHybrid(ctx, "任意问题", 3, 0.7)
	if err != nil || len(got) != 0 {
		t.Fatalf("空索引应返回 nil, nil，实际 %v, %v", got, err)
	}
}
