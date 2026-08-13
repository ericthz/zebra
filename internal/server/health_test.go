package server

import (
	"errors"
	"net/http/httptest"
	"testing"
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
