package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/kg"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/tool"
)

// fakeEmbedder 确定性嵌入（与 rag 包测试同构）：字符出现即置位。
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, 1024)
	for _, r := range text {
		vec[int(r)%1024] = 1
	}
	return vec, nil
}

// countingReranker 记录是否被调用并原样返回（不改变结果，只验证接线）。
type countingReranker struct{ called bool }

func (c *countingReranker) Rerank(_ context.Context, _ string, hits []rag.Result) ([]rag.Result, error) {
	c.called = true
	return hits, nil
}

// TestRAGRerankerInvoked 验证：配置了 Reranker 时，Agent 检索走精排路径。
func TestRAGRerankerInvoked(t *testing.T) {
	idx := rag.NewIndex(fakeEmbedder{})
	idx.AddDocument(context.Background(),
		"zebra 是企业级 AI Agent 参考项目，提供工具调用、记忆、技能与 RAG 能力。", "zebra.md", 100, 0)

	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})

	fp := &fakeProvider{}
	rr := &countingReranker{}
	ag := New(Config{
		Router: provider.NewRouter(fp), Tools: tool.NewRegistry(), Prompts: prompts,
		MaxTurns: 1, PromptName: "assistant", RAG: idx, Reranker: rr,
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	if _, err := ag.Run(context.Background(), "zebra 有什么能力？", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if !rr.called {
		t.Fatal("配置了 Reranker 却未走 RetrieveReranked 精排路径")
	}

	injected := false
	for _, m := range fp.lastMsgs {
		if m.Role == "system" && strings.Contains(m.Content, "zebra.md") {
			injected = true
		}
	}
	if !injected {
		t.Fatalf("RAG 片段未注入，实际消息: %+v", fp.lastMsgs)
	}
}

// TestKGInjectedIntoMessages 验证：图谱有实体关系时，被注入为 system 消息（P52 接线）。
func TestKGInjectedIntoMessages(t *testing.T) {
	graph := kg.NewGraph()
	graph.Add(kg.Triple{Subject: "zebra", Predicate: "支持", Object: "工具调用"})

	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})

	fp := &fakeProvider{}
	ag := New(Config{
		Router: provider.NewRouter(fp), Tools: tool.NewRegistry(), Prompts: prompts,
		MaxTurns: 1, PromptName: "assistant", KG: graph,
	})
	hist := make([]provider.Message, 0)
	ag.Bind("s", "admin", "u", &hist)

	if _, err := ag.Run(context.Background(), "zebra 有什么能力？", RunOptions{}); err != nil {
		t.Fatal(err)
	}

	injected := false
	for _, m := range fp.lastMsgs {
		if m.Role == "system" && strings.Contains(m.Content, "zebra 支持 工具调用") {
			injected = true
		}
	}
	if !injected {
		t.Fatalf("图谱关系未注入，实际消息: %+v", fp.lastMsgs)
	}
}
