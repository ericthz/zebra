package memory

import (
	"io"
	"log/slog"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSetupManagerWithoutQdrant(t *testing.T) {
	t.Setenv("QDRANT_URL", "")
	mem, long := SetupManager(discardLogger())
	if mem == nil || mem.Working == nil || mem.Long != nil {
		t.Fatalf("未配置 Qdrant 应仅工作记忆: %+v", mem)
	}
	if long {
		t.Fatal("未配置 Qdrant 不应启用长期记忆")
	}
}

func TestSetupManagerDegrade(t *testing.T) {
	// Qdrant 与 Ollama 均指向不可达地址 → 探针失败 → 自动降级
	t.Setenv("QDRANT_URL", "http://127.0.0.1:1")
	t.Setenv("OLLAMA_BASE_URL", "http://127.0.0.1:1")
	mem, long := SetupManager(discardLogger())
	if mem == nil {
		t.Fatal("管理器不应为 nil")
	}
	if long {
		t.Fatal("Qdrant 不可达时应降级为仅工作记忆")
	}
}

func TestNewEmbedderFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "k")
	if _, ok := NewEmbedderFromEnv().(*OpenAIEmbedder); !ok {
		t.Fatal("设置 OPENAI_API_KEY 应返回 OpenAIEmbedder")
	}
	t.Setenv("OPENAI_API_KEY", "")
	if _, ok := NewEmbedderFromEnv().(*OllamaEmbedder); !ok {
		t.Fatal("未设置 OPENAI_API_KEY 应返回 OllamaEmbedder")
	}
}

func TestVectorSizeEnv(t *testing.T) {
	t.Setenv("EMBED_VECTOR_SIZE", "512")
	if vectorSize() != 512 {
		t.Fatalf("应读取 512，实际 %d", vectorSize())
	}
	t.Setenv("EMBED_VECTOR_SIZE", "abc")
	if vectorSize() != 768 {
		t.Fatalf("非法值应回退 768，实际 %d", vectorSize())
	}
	t.Setenv("EMBED_VECTOR_SIZE", "")
	if vectorSize() != 768 {
		t.Fatalf("空值应回退 768，实际 %d", vectorSize())
	}
}
