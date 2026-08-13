package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/kg"
)

func TestKnowledgeAPI(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "k", User: "u", Role: "user", Tenant: "default"})
	graph := kg.NewGraph()
	graph.Add(kg.Triple{Subject: "zebra", Predicate: "支持", Object: "工具调用"})
	api := NewAPIServer(Deps{
		Keys:    keys,
		Rate:    NewRateLimiter(100, 100),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics: NewMetrics(),
		KG:      graph,
	})
	h := api.Handler()

	do := func(path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 查询实体 → 返回相关三元组
	rr := do("/v1/knowledge?entity=工具调用", "k")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "zebra") {
		t.Fatalf("知识查询异常: %d %s", rr.Code, rr.Body.String())
	}
	// 缺 entity → 400；未鉴权 → 401
	if rr := do("/v1/knowledge", "k"); rr.Code != http.StatusBadRequest {
		t.Fatalf("缺 entity 应 400，实际 %d", rr.Code)
	}
	if rr := do("/v1/knowledge?entity=工具调用", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权应 401，实际 %d", rr.Code)
	}
}
