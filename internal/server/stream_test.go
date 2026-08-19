package server

import (
	"bytes"
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

// flushRecorder 计数底层 Flush 调用：用于断言 SSE 流式事件真正逐条下发，
// 而非被 HTTP 层缓冲后一次性吐出（六3）。
type flushRecorder struct {
	*httptest.ResponseRecorder
	mu      sync.Mutex
	flushes int
}

func (r *flushRecorder) Flush() {
	r.mu.Lock()
	r.flushes++
	r.mu.Unlock()
}

func (r *flushRecorder) flushCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushes
}

// TestSSEStreamFlushesThroughMiddleware 验证 statusRecorder 实现了 http.Flusher：
// /v1/chat/stream 经 AccessLog 中间件包裹后仍能逐条 Flush（六3 回归）。
func TestSSEStreamFlushesThroughMiddleware(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "主回答：北京 25 度，晴朗。", name: "primary-model"}),
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
	h := api.Handler() // 完整中间件链（AccessLog 用 statusRecorder 包裹）

	// 先建会话
	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(`{"message":"你好"}`))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("建会话应 200，实际 %d", rr.Code)
	}
	var first ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}

	// 流式请求：走 plan 模式（每次 emit 一个事件，处理器逐条 Flush）
	body, _ := json.Marshal(map[string]string{"session_id": first.SessionID, "message": "今天天气如何", "mode": "plan"})
	sreq := httptest.NewRequest("POST", "/v1/chat/stream", bytes.NewBuffer(body))
	sreq.Header.Set("Authorization", "Bearer user-key")
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rec, sreq)

	if rec.Code != http.StatusOK {
		t.Fatalf("stream 应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	// 六3：若 statusRecorder 未实现 Flush，处理器断言 (http.Flusher) 恒失败，
	// 事件被缓冲，flushCount 恒 0。
	if rec.flushCount() == 0 {
		t.Fatalf("SSE 事件未被逐条 Flush（statusRecorder 未透传 http.Flusher）")
	}
	// 且必须实际收到完成事件（证明流式结果确实送达）
	if !strings.Contains(rec.Body.String(), "event: done") {
		t.Fatalf("流式响应缺少 done 事件: %s", rec.Body.String())
	}
}
