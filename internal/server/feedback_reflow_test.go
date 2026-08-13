package server

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/feedback"
	"github.com/ericthz/zebra/internal/provider"
)

// TestFeedbackReflow P55：负面反馈自动把问答对追加进评测数据集。
func TestFeedbackReflow(t *testing.T) {
	casesDir := t.TempDir()
	keys := NewKeyStore()
	keys.Register(Principal{Key: "k", User: "u", Role: "user", Tenant: "default"})
	sessions := NewInMemoryStore(time.Minute)
	api := NewAPIServer(Deps{
		Keys:         keys,
		Rate:         NewRateLimiter(100, 100),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:      NewMetrics(),
		Sessions:     sessions,
		Feedback:     feedback.NewInMemoryStore(),
		EvalCasesDir: casesDir,
	})
	h := api.Handler()

	// 造一个含问答历史的会话
	sess, _ := sessions.Create("u", "default", "user", time.Minute)
	*sess.History() = append(*sess.History(),
		provider.Message{Role: "user", Content: "北京天气怎么样？"},
		provider.Message{Role: "assistant", Content: "北京今天晴。"},
	)

	body := fmt.Sprintf(`{"session_id":%q,"rating":-1,"comment":"答非所问"}`, sess.ID)
	req := httptest.NewRequest("POST", "/v1/feedback", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer k")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("反馈提交应 200，实际 %d %s", rr.Code, rr.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(casesDir, "feedback.json"))
	if err != nil || !strings.Contains(string(data), "北京天气怎么样？") || !strings.Contains(string(data), "feedback") {
		t.Fatalf("负面反馈应回流进数据集: %v %s", err, data)
	}

	// 正面反馈不回流
	body = fmt.Sprintf(`{"session_id":%q,"rating":1}`, sess.ID)
	req = httptest.NewRequest("POST", "/v1/feedback", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer k")
	h.ServeHTTP(httptest.NewRecorder(), req)
	data, _ = os.ReadFile(filepath.Join(casesDir, "feedback.json"))
	if n := strings.Count(string(data), `"id": "fb-`); n != 1 {
		t.Fatalf("正面反馈不应回流，应只有 1 条回流用例，实际 %d", n)
	}
}
