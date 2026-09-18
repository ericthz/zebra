package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeEmbedder 返回固定向量，供 Qdrant 测试。
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(context.Context, string) ([]float32, error) {
	v := make([]float32, 4)
	for i := range v {
		v[i] = float32(i)
	}
	return v, nil
}

func TestQdrantForgetUserUsesFilterDelete(t *testing.T) {
	var hit struct {
		path   string
		method string
		filter map[string]interface{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.path, hit.method = r.URL.Path, r.Method
		if r.URL.Path == "/collections/mem/points/delete" {
			var body struct {
				Filter map[string]interface{} `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				hit.filter = body.Filter
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := NewQdrantMemory(srv.URL, "mem", 4, fakeEmbedder{})
	ctx := context.Background()

	if err := m.ForgetUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if hit.path != "/collections/mem/points/delete" || hit.method != http.MethodPost {
		t.Fatalf("应按用户走 points/delete 过滤删除，实际 %s %s", hit.method, hit.path)
	}
	// 断言 filter 包含 user=alice 的匹配条件
	must, _ := hit.filter["must"].([]interface{})
	if len(must) != 1 {
		t.Fatalf("filter.must 应有 1 条 user 条件，实际 %v", hit.filter)
	}
}

func TestQdrantForgetTenantDeletesTenantCollection(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := NewQdrantMemory(srv.URL, "mem", 4, fakeEmbedder{})
	ctx := context.Background()

	if err := m.ForTenant("ta").Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/collections/mem_ta" {
		t.Fatalf("应按租户删除 mem_ta，实际 %s", gotPath)
	}
}

// TestQdrantRetrieveFiltersByUser：检索必须携带 user=alice 的 filter
// 否则会命中同租户其他用户的记忆。
func TestQdrantRetrieveFiltersByUser(t *testing.T) {
	var lastBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/collections/mem/points/search" {
			_ = json.NewDecoder(r.Body).Decode(&lastBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	m := NewQdrantMemory(srv.URL, "mem", 4, fakeEmbedder{})
	ctx := context.Background()

	if _, err := m.Retrieve(ctx, "alice", "火锅", 5); err != nil {
		t.Fatal(err)
	}
	must, ok := lastBody["filter"].(map[string]interface{})["must"].([]interface{})
	if !ok || len(must) != 1 {
		t.Fatalf("应携带按 user 过滤的 filter: %v", lastBody["filter"])
	}
	cond := must[0].(map[string]interface{})
	if cond["key"] != "user" || cond["match"].(map[string]interface{})["value"] != "alice" {
		t.Fatalf("filter 应为 user=alice: %v", cond)
	}

	// 空 user（自检等内部调用）不应带 filter
	lastBody = nil
	if _, err := m.Retrieve(ctx, "", "ping", 1); err != nil {
		t.Fatal(err)
	}
	if lastBody["filter"] != nil {
		t.Fatalf("空 user 不应带 filter: %v", lastBody["filter"])
	}
}
