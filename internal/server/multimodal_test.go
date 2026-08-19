package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// capProvider 记录收到的最新消息并固定回复（断言图片到达 provider）。
type capProvider struct {
	mu   sync.Mutex
	last []provider.Message
}

func (p *capProvider) Name() string { return "cap" }
func (p *capProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	p.mu.Lock()
	p.last = msgs
	p.mu.Unlock()
	return provider.Message{Content: "看到了图片"}, nil
}
func (p *capProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func (p *capProvider) contentParts() []provider.Part {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.last {
		if m.Role == "user" && len(m.ContentParts) > 0 {
			return m.ContentParts
		}
	}
	return nil
}

// TestChatImagesReachProvider C14 端到端：/v1/chat 的 images 字段经 Agent
// 组装成 ContentParts，最终到达 provider（消除"字段有、模型没收到图"的半实现）。
func TestChatImagesReachProvider(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})
	cap := &capProvider{}

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(cap),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
	})
	h := api.Handler()

	body, _ := json.Marshal(map[string]interface{}{
		"message": "这张图里有什么",
		"images":  []string{"data:image/png;base64,AAAA", "https://example.com/b.jpg"},
	})
	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", rr.Code, rr.Body.String())
	}
	parts := cap.contentParts()
	if parts == nil {
		t.Fatal("provider 未收到带 ContentParts 的用户消息（图片没传进去）")
	}
	if len(parts) != 3 {
		t.Fatalf("应为 text+2×image，实际 %d 块: %+v", len(parts), parts)
	}
	if parts[0].Type != "text" || !strings.Contains(parts[0].Text, "这张图里有什么") {
		t.Fatalf("首块应为 text 问题: %+v", parts[0])
	}
	if parts[1].ImageURL != "data:image/png;base64,AAAA" || parts[2].ImageURL != "https://example.com/b.jpg" {
		t.Fatalf("图片 URL 透传错误: %+v", parts)
	}
}

// TestChatImagesReachProviderStream 流式端到端：images 同样到达 provider。
func TestChatImagesReachProviderStream(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})
	cap := &capProvider{}

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(cap),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
	})
	h := api.Handler()

	body, _ := json.Marshal(map[string]interface{}{
		"message": "看图描述",
		"images":  []string{"https://example.com/a.png"},
	})
	req := httptest.NewRequest("POST", "/v1/chat/stream", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "看到了图片") {
		t.Fatalf("流式应输出回答: %s", rr.Body.String())
	}
	parts := cap.contentParts()
	if parts == nil {
		t.Fatal("流式下 provider 未收到图片内容块")
	}
}
