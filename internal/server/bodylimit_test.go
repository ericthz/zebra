package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// TestMaxJSONBodyRejected 六7：JSON 请求体超过 maxJSONBody 必须 413，
// 不能任其被 Decode 吃光内存。
func TestMaxJSONBodyRejected(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "25 度，晴朗。"}),
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

	// 2MB 请求体 >> maxJSONBody(1MB)
	huge := `{"message":"` + strings.Repeat("a", 2<<20) + `"}`
	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(huge))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求体应 413，实际 %d body=%s", rr.Code, rr.Body.String())
	}
}
