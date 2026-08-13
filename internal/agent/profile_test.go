package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
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
