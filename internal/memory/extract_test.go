package memory

import (
	"context"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// factsProvider 固定返回结构化 JSON 的抽取模型（模拟 LLM 语义抽取）。
type factsProvider struct{ reply string }

func (f *factsProvider) Name() string { return "facts-llm" }
func (f *factsProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: f.reply}, nil
}
func (f *factsProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestRuleExtractor(t *testing.T) {
	e := RuleExtractor{}
	facts, err := e.Extract(context.Background(), "我叫小明，我住在北京。")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("规则抽取应命中 2 条，实际 %d: %+v", len(facts), facts)
	}
}

func TestLLMExtractorSuccess(t *testing.T) {
	e := &LLMExtractor{
		Router: provider.NewRouter(&factsProvider{
			reply: `{"facts":[{"key":"work","value":"程序员","confidence":0.9},{"key":"preference","value":"打篮球","confidence":0.85}]}`,
		}),
	}
	facts, err := e.Extract(context.Background(), "我是北京的程序员，喜欢打篮球。")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 || facts[0].Key != "work" || facts[1].Value != "打篮球" {
		t.Fatalf("LLM 抽取结果异常: %+v", facts)
	}
}

func TestLLMExtractorFallback(t *testing.T) {
	// LLM 返回非 JSON → 回退规则：仍能从"我叫小明"抽到 name
	e := &LLMExtractor{Router: provider.NewRouter(&factsProvider{reply: "完全不是 JSON"})}
	facts, err := e.Extract(context.Background(), "我叫小明。")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Key != "name" || facts[0].Value != "小明" {
		t.Fatalf("回退规则抽取异常: %+v", facts)
	}

	// LLM 返回空 facts → 同样回退规则
	e2 := &LLMExtractor{Router: provider.NewRouter(&factsProvider{reply: `{"facts":[]}`})}
	facts, err = e2.Extract(context.Background(), "我叫小红。")
	if err != nil || len(facts) != 1 || facts[0].Value != "小红" {
		t.Fatalf("空结果回退异常: %v %+v", err, facts)
	}
}
