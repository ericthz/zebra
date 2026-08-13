package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

func TestProfileInjection(t *testing.T) {
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra 助手。"})
	profile := memory.NewProfileStore()
	now := time.Now()
	profile.Learn("alice", []memory.Fact{
		{Key: "name", Value: "小明", Confidence: 0.95},
		{Key: "preference", Value: "火锅", Confidence: 0.85},
	}, now)

	ag := New(Config{Prompts: prompts, Profile: profile, ProfileTTL: time.Hour})
	ag.Bind("sess-1", "user", "alice", nil)

	msgs := ag.buildMessages(context.Background(), "你好")
	var profileMsg string
	for _, m := range msgs {
		if m.Role == "system" && strings.Contains(m.Content, "画像") {
			profileMsg = m.Content
		}
	}
	if profileMsg == "" {
		t.Fatal("应注入画像 system 消息")
	}
	if !strings.Contains(profileMsg, "小明") || !strings.Contains(profileMsg, "火锅") {
		t.Fatalf("画像内容缺失: %s", profileMsg)
	}

	// TTL 过期后不再注入（读时惰性遗忘）
	ag.cfg.ProfileTTL = time.Nanosecond
	msgs = ag.buildMessages(context.Background(), "你好")
	for _, m := range msgs {
		if strings.Contains(m.Content, "画像") {
			t.Fatalf("TTL 过期后不应注入画像: %s", m.Content)
		}
	}

	// 用户隔离：bob 无画像 → 不注入
	ag.Bind("sess-2", "user", "bob", nil)
	ag.cfg.ProfileTTL = time.Hour
	for _, m := range ag.buildMessages(context.Background(), "你好") {
		if strings.Contains(m.Content, "画像") {
			t.Fatalf("bob 不应看到 alice 画像")
		}
	}
}

// factsProvider 返回结构化画像 JSON（模拟 LLM 语义抽取模型）。
type factsProvider struct{ reply string }

func (f *factsProvider) Name() string { return "facts-llm" }
func (f *factsProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: f.reply}, nil
}
func (f *factsProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestProfileLearnViaLLMExtractor 对话中用 LLM 抽取画像（P27）。
func TestProfileLearnViaLLMExtractor(t *testing.T) {
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra 助手。"})
	router := provider.NewRouter(&factsProvider{
		reply: `{"facts":[{"key":"name","value":"大明","confidence":0.9}]}`,
	})
	profile := memory.NewProfileStore()
	hist := []provider.Message{}

	ag := New(Config{
		Router:     router,
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Profile:    profile,
		ProfileTTL: time.Hour,
		PromptName: "assistant",
		Extractor:  &memory.LLMExtractor{Router: router},
	})
	ag.Bind("sess-1", "user", "alice", &hist)

	if _, err := ag.Run(context.Background(), "你记住，我叫大明。", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if profile.Count("alice") != 1 {
		t.Fatalf("应学到 1 条画像事实，实际 %d", profile.Count("alice"))
	}
	facts := profile.FactsFor("alice", time.Now(), time.Hour)
	if len(facts) != 1 || facts[0].Key != "name" || facts[0].Value != "大明" {
		t.Fatalf("LLM 抽取结果未入库: %+v", facts)
	}
}
