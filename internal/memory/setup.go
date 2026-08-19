// 记忆装配（P32）：按环境变量构建分层记忆，供 cmd/server 与 cmd/zebra 共用。
//
// 背景：两个入口此前各自装配记忆，Zebra CLI 只启工作记忆、与 server 行为
// 不一致。抽成共享 helper 后两端逻辑天然一致：配置了 QDRANT_URL 且探针
// 通过 → 工作记忆 + Qdrant 长期记忆；否则自动降级为仅工作记忆（B7）。
//
// 环境变量（与 README §5 对齐）：
//
//	QDRANT_URL          长期记忆地址（设置即尝试启用）
//	QDRANT_COLLECTION   集合名（默认 zebra_mem）
//	EMBED_VECTOR_SIZE   向量维度（默认 768，须与嵌入模型匹配）
//	OLLAMA_*/OPENAI_*   嵌入器选择（见 NewEmbedderFromEnv）
package memory

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ericthz/zebra/internal/redis"
)

// SetupManager 按环境变量装配分层记忆，返回（管理器, 是否启用长期记忆）。
func SetupManager(logger *slog.Logger) (*Manager, bool) {
	working := NewWorkingMemory(10)
	mem := NewManager(working, nil) // 先只启工作记忆

	q := os.Getenv("QDRANT_URL")
	if q == "" {
		return mem, false
	}
	qmem := NewQdrantMemory(q, envOr("QDRANT_COLLECTION", "zebra_mem"), vectorSize(), NewEmbedderFromEnv())
	// 就绪探针：先 ensure 集合（首启自动创建，幂等），再检索验证读写链路；
	// Qdrant 不可用（连接失败/嵌入服务不可用）时自动降级为仅工作记忆（B7）。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := qmem.ensure(ctx); err != nil {
		logger.Warn("Qdrant 不可用，降级为仅工作记忆", "err", err)
		return mem, false
	}
	_, perr := qmem.Retrieve(ctx, "", "ping", 1)
	if perr == nil {
		mem = NewManager(working, qmem)
		logger.Info("长期记忆已启用", "qdrant", q, "collection", envOr("QDRANT_COLLECTION", "zebra_mem"))
		return mem, true
	}
	logger.Warn("Qdrant 检索探针失败，降级为仅工作记忆", "err", perr)
	return mem, false
}

// NewEmbedderFromEnv 按环境变量构造嵌入器：
// 设置 OPENAI_API_KEY → OpenAI 兼容；否则 Ollama 本地。
func NewEmbedderFromEnv() Embedder {
	base := envOr("OLLAMA_BASE_URL", "http://localhost:11434")
	model := envOr("EMBED_MODEL", "nomic-embed-text:v1.5")
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		return &OpenAIEmbedder{
			BaseURL: envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			Model:   envOr("OPENAI_EMBED_MODEL", "text-embedding-3-small"),
			APIKey:  apiKey,
			Client:  &http.Client{Timeout: 10 * time.Second},
		}
	}
	return &OllamaEmbedder{BaseURL: base, Model: model, Client: &http.Client{Timeout: 10 * time.Second}}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func vectorSize() int {
	n, err := strconv.Atoi(envOr("EMBED_VECTOR_SIZE", "768"))
	if err != nil || n <= 0 {
		return 768
	}
	return n
}

// SetupManagerRedis 按环境变量装配"Redis 长期记忆"分层记忆（P51）。
// 先写读探针 key 验证连通性；失败自动降级为仅工作记忆（B7）。
func SetupManagerRedis(client *redis.Client, logger *slog.Logger) (*Manager, bool) {
	working := NewWorkingMemory(10)
	mem := NewManager(working, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	probeKey := redisMemPrefix + "probe"
	if err := client.Set(ctx, probeKey, "1", time.Second); err != nil {
		logger.Warn("Redis 不可用，长期记忆保持关闭", "err", err)
		return mem, false
	}
	if _, ok, err := client.Get(ctx, probeKey); err != nil || !ok {
		logger.Warn("Redis 探针失败，长期记忆保持关闭", "err", err)
		return mem, false
	}
	mem = NewManager(working, NewRedisMemory(client, "default"))
	logger.Info("长期记忆已启用（Redis）")
	return mem, true
}
