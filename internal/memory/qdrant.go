// 长期记忆：Qdrant 向量数据库（纯 HTTP 手写，无 SDK）。
//
// 多租户隔离：QdrantMemory.ForTenant(tenant) 返回一个 collection
// 名带租户后缀的实例，各租户向量互不可见。生产可用 Qdrant payload filter
// 或独立实例替换，接口不变。
package memory

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// QdrantMemory 长期记忆实现。
type QdrantMemory struct {
	baseURL  string // Qdrant HTTP 地址，如 http://localhost:6333
	collect  string // 集合名（含租户后缀）
	vector   int    // 向量维度，须与嵌入模型匹配
	embed    Embedder
	client   *http.Client
	mu       sync.Mutex
	initOnce bool
}

// NewQdrantMemory 构造。
func NewQdrantMemory(baseURL, collection string, vector int, embed Embedder) *QdrantMemory {
	return &QdrantMemory{
		baseURL: baseURL,
		collect: collection,
		vector:  vector,
		embed:   embed,
		client:  &http.Client{},
	}
}

// 编译期断言：实现 Memory、TenantScoped 与 UserScoped（被遗忘权）。
var (
	_ Memory       = (*QdrantMemory)(nil)
	_ TenantScoped = (*QdrantMemory)(nil)
	_ UserScoped   = (*QdrantMemory)(nil)
)

// ForTenant 返回绑定到指定租户的隔离实例。
// 逐字段克隆（不复制内部锁），共享底层 HTTP client 与嵌入器。
func (m *QdrantMemory) ForTenant(tenant string) Memory {
	return &QdrantMemory{
		baseURL: m.baseURL,
		collect: m.collect + "_" + sanitize(tenant),
		vector:  m.vector,
		embed:   m.embed,
		client:  m.client,
	}
}

// Store 写入一条记忆（向量 + 元数据）。
func (m *QdrantMemory) Store(ctx context.Context, content string, meta map[string]string) error {
	vec, err := m.embed.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("embedding 失败: %w", err)
	}
	payload := map[string]interface{}{"content": content}
	for k, v := range meta {
		payload[k] = v
	}
	return m.upsert(ctx, []map[string]interface{}{{
		"id": generateID(), "vector": vec, "payload": payload,
	}})
}

// Retrieve 语义检索属于指定 user 的记忆（用户隔离）。
// Qdrant payload 里存了 user（Store 写入），检索时用 filter 精确匹配，
// 防止检索到同租户其他用户的对话（纵深防御）。
func (m *QdrantMemory) Retrieve(ctx context.Context, user, query string, limit int) ([]string, error) {
	vec, err := m.embed.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding 失败: %w", err)
	}
	body := map[string]interface{}{
		"vector": vec, "limit": limit, "with_payload": true,
	}
	if user != "" {
		body["filter"] = map[string]interface{}{
			"must": []map[string]interface{}{{
				"key": "user", "match": map[string]interface{}{"value": user},
			}},
		}
	}
	raw, _ := json.Marshal(body)
	resp, err := m.do(ctx, http.MethodPost, m.url("/points/search"), raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Result []struct {
			Payload map[string]interface{} `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var contents []string
	for _, r := range out.Result {
		if s, ok := r.Payload["content"].(string); ok {
			contents = append(contents, s)
		}
	}
	return contents, nil
}

// Clear 删除当前租户的集合（清空记忆）。
func (m *QdrantMemory) Clear(ctx context.Context) error {
	resp, err := m.do(ctx, http.MethodDelete, m.url(""), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	m.mu.Lock()
	m.initOnce = false
	m.mu.Unlock()
	return nil
}

// ForgetUser 按用户删除全部记忆（被遗忘权）。
// Qdrant 支持按 payload filter 删除点，只删该用户的记录，不误伤同集合其他用户。
func (m *QdrantMemory) ForgetUser(ctx context.Context, user string) error {
	filter := map[string]interface{}{
		"must": []map[string]interface{}{{
			"key": "user", "match": map[string]interface{}{"value": user},
		}},
	}
	body, _ := json.Marshal(map[string]interface{}{"filter": filter})
	resp, err := m.do(ctx, http.MethodPost, m.url("/points/delete"), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ---- 内部：Qdrant HTTP 交互 ----

func (m *QdrantMemory) url(suffix string) string {
	return m.baseURL + "/collections/" + m.collect + suffix
}

func (m *QdrantMemory) do(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("qdrant %d: %s", resp.StatusCode, string(b))
	}
	return resp, nil
}

func (m *QdrantMemory) ensure(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.initOnce {
		return nil
	}
	// 检查集合是否存在
	resp, err := m.do(ctx, http.MethodGet, m.url(""), nil)
	if err == nil {
		resp.Body.Close()
		m.initOnce = true
		return nil
	}
	// 创建集合
	body, _ := json.Marshal(map[string]interface{}{
		"vectors": map[string]interface{}{"size": m.vector, "distance": "Cosine"},
	})
	resp, err = m.do(ctx, http.MethodPut, m.url(""), body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	m.initOnce = true
	return nil
}

func (m *QdrantMemory) upsert(ctx context.Context, points []map[string]interface{}) error {
	if err := m.ensure(ctx); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]interface{}{"points": points})
	resp, err := m.do(ctx, http.MethodPut, m.url("/points"), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
