package memory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmbeddersCheckStatus 验证：非 200 响应返回错误而非静默解析空结果。
func TestEmbeddersCheckStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	openai := &OpenAIEmbedder{BaseURL: ts.URL, Model: "m", Client: ts.Client()}
	if _, err := openai.Embed(context.Background(), "hi"); err == nil {
		t.Fatal("OpenAIEmbedder 非 200 应报错")
	}
	ollama := &OllamaEmbedder{BaseURL: ts.URL, Model: "m", Client: ts.Client()}
	if _, err := ollama.Embed(context.Background(), "hi"); err == nil {
		t.Fatal("OllamaEmbedder 非 200 应报错")
	}
}

// TestOpenAIEmbedderOK 验证：200 正常返回向量。
func TestOpenAIEmbedderOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer ts.Close()

	e := &OpenAIEmbedder{BaseURL: ts.URL, Model: "m", Client: ts.Client()}
	v, err := e.Embed(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 3 || v[0] != 0.1 || v[2] != 0.3 {
		t.Fatalf("向量解析异常: %v", v)
	}
}
