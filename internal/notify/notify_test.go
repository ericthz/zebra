package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// fakeRoundTripper 进程内拦截 HTTP 请求（无需真实端口，可在无网 CI 运行）。
type fakeRoundTripper struct {
	statusCode int
	attempts   int
	lastReq    *http.Request
	lastBody   []byte
	keys       []string // 每次请求携带的幂等键（验证重试时不变）
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	f.attempts++
	f.lastReq = req
	f.keys = append(f.keys, req.Header.Get("X-Idempotency-Key"))
	body, _ := io.ReadAll(req.Body)
	f.lastBody = body
	return &http.Response{
		StatusCode: f.statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}, nil
}

func newTestNotifier(rt *fakeRoundTripper) *WebhookNotifier {
	n := NewWebhookNotifier("http://fake.local/hook", "secret-key")
	n.Client = &http.Client{Transport: rt}
	n.Backoff = 5 * time.Millisecond
	return n
}

// TestWebhookDelivered 验证：事件正确送达 + 幂等键/签名头存在。
func TestWebhookDelivered(t *testing.T) {
	rt := &fakeRoundTripper{statusCode: 200}
	n := newTestNotifier(rt)

	ev := Event{Type: "task.complete", Session: "s1", Title: "报告完成", Payload: map[string]any{"ok": true}}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}

	var got Event
	if err := json.Unmarshal(rt.lastBody, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "task.complete" || got.Title != "报告完成" {
		t.Fatalf("事件未正确送达: %+v", got)
	}
	if rt.lastReq.Header.Get("X-Idempotency-Key") == "" {
		t.Fatal("应带幂等键")
	}
	if rt.lastReq.Header.Get("X-Signature") == "" {
		t.Fatal("应带 HMAC 签名")
	}
}

// TestWebhookRetry 验证：5xx 会重试，最终失败返回错误。
func TestWebhookRetry(t *testing.T) {
	rt := &fakeRoundTripper{statusCode: 500}
	n := newTestNotifier(rt)
	n.MaxRetries = 2

	if err := n.Send(context.Background(), Event{Type: "x"}); err == nil {
		t.Fatal("5xx 重试后仍失败应报错")
	}
	if rt.attempts != 3 { // 1 次 + 2 次重试
		t.Fatalf("期望 3 次尝试，实际 %d", rt.attempts)
	}
	// 同一事件的所有重试必须携带同一个幂等键，接收方才能去重
	if len(rt.keys) != 3 {
		t.Fatalf("期望记录 3 个幂等键，实际 %d", len(rt.keys))
	}
	for i := 1; i < len(rt.keys); i++ {
		if rt.keys[i] == "" || rt.keys[i] != rt.keys[0] {
			t.Fatalf("重试时幂等键应保持不变: %v", rt.keys)
		}
	}
}
