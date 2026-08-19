package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicMaxTokensConfigurable 验证：MaxTokens 可配置，且请求体携带该值。
func TestAnthropicMaxTokensConfigurable(t *testing.T) {
	var gotMaxTokens int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotMaxTokens = body.MaxTokens
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"content":[{"type":"text","text":"hi"}]}`))
	}))
	defer ts.Close()

	cli := NewHTTPClient(0, 1, 0)
	// 默认 2048
	p := &AnthropicProvider{BaseURL: ts.URL, Model: "claude", APIKey: "k", Client: cli}
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if gotMaxTokens != 2048 {
		t.Fatalf("默认 max_tokens 应 2048，实际 %d", gotMaxTokens)
	}

	// 可配置 4096
	p.MaxTokens = 4096
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if gotMaxTokens != 4096 {
		t.Fatalf("配置 max_tokens 应 4096，实际 %d", gotMaxTokens)
	}
}
