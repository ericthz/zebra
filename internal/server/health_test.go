package server

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ericthz/zebra/internal/cost"
)

var errTest = errors.New("test dependency failed")

func TestHealthzAndReadyz(t *testing.T) {
	h := HealthzHandler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != 200 {
		t.Fatalf("healthz 应 200，实际 %d", rr.Code)
	}

	ok := ReadyzHandler(nil, map[string]func() error{"db": func() error { return nil }})
	rr = httptest.NewRecorder()
	ok.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
	if rr.Code != 200 {
		t.Fatalf("readyz 应 200，实际 %d", rr.Code)
	}

	bad := ReadyzHandler(nil, map[string]func() error{"db": func() error { return errTest }})
	rr = httptest.NewRecorder()
	bad.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
	if rr.Code != 503 {
		t.Fatalf("依赖不可用时 readyz 应 503，实际 %d", rr.Code)
	}
}

func TestMetricsSnapshot(t *testing.T) {
	m := NewMetrics()
	m.Inc("http:requests")
	m.Observe("http:latency", 5_000_000) // 5ms
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	body := rr.Body.String()
	if body == "" {
		t.Fatal("metrics 不应为空")
	}
	t.Log(body)
}

// TestMetricsCostRequiresAdmin P1-9：/metrics/cost 含 per-user/per-session
// 成本明细，禁止公开。未带密钥 401；普通用户 403；admin 才能访问。
func TestMetricsCostRequiresAdmin(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "admin-key", User: "admin", Role: "admin", Tenant: "default"})
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})

	tracker := cost.NewTracker()
	tracker.Record("alice", "s1", "gpt-4o-mini", 100, 50)

	api := NewAPIServer(Deps{
		Keys:     keys,
		Cost:     tracker,
		Metrics:  NewMetrics(),
		Rate:     NewRateLimiter(100, 100),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxTurns: 5,
	})
	h := api.Handler()

	do := func(key string) int {
		req := httptest.NewRequest("GET", "/metrics/cost", nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := do(""); code != http.StatusUnauthorized {
		t.Fatalf("未认证访问 /metrics/cost 应 401，实际 %d", code)
	}
	if code := do("user-key"); code != http.StatusForbidden {
		t.Fatalf("普通用户访问 /metrics/cost 应 403，实际 %d", code)
	}
	if code := do("admin-key"); code != http.StatusOK {
		t.Fatalf("admin 访问 /metrics/cost 应 200，实际 %d", code)
	}
}
