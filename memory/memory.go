// memory/memory.go
package memory

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Memory 定义长期记忆存储与检索接口
type Memory interface {
	Store(content string, metadata map[string]string) error
	Retrieve(query string, limit int) ([]string, error)
	Clear() error
}

// ---------- Qdrant HTTP 交互结构 ----------

type qdrantCreateCollection struct {
	Vectors qdrantVectorParams `json:"vectors"`
}

type qdrantVectorParams struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

type qdrantUpsertPoints struct {
	Points []qdrantPoint `json:"points"`
}

type qdrantPoint struct {
	ID      string                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]interface{} `json:"payload"`
}

type qdrantSearchRequest struct {
	Vector      []float32 `json:"vector"`
	Limit       int       `json:"limit"`
	WithPayload bool      `json:"with_payload"`
}

type qdrantSearchResponse struct {
	Result []struct {
		ID      string                 `json:"id"`
		Payload map[string]interface{} `json:"payload"`
		Score   float32                `json:"score"`
	} `json:"result"`
}

// ---------- 嵌入请求/响应结构 ----------

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
}

type openAIEmbedRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// ---------- QdrantMemory 实现 ----------

type QdrantMemory struct {
	qdrantURL        string // Qdrant 服务地址，例如 http://localhost:6333
	collectionName   string // 集合名称
	vectorSize       int    // 向量维度（需与嵌入模型输出匹配）
	distance         string // 距离度量，通常 "Cosine"
	ollamaURL        string // Ollama 地址（用于 ollama 嵌入）
	embedModel       string // 嵌入模型名称
	embedProvider    string // 嵌入提供者："ollama" 或 "openai"
	embedAPIKey      string // API 密钥（用于 openai 等）
	openaiBaseURL    string // OpenAI 兼容端点的基础 URL（默认 https://api.openai.com/v1）
	client           *http.Client
	mu               sync.RWMutex
	collectionInited bool
}

// NewQdrantMemory 创建一个生产级 Qdrant 记忆实例（兼容旧版调用）。
// 嵌入提供者通过环境变量配置：
//
//	EMBED_PROVIDER  : ollama (默认) 或 openai
//	OPENAI_API_KEY  : OpenAI API 密钥（openai 提供者时必需）
//	OPENAI_BASE_URL : OpenAI 兼容端点基础 URL（可选，默认 https://api.openai.com/v1）
func NewQdrantMemory(qdrantURL, collectionName, ollamaURL, embedModel string, vectorSize int) *QdrantMemory {
	provider := strings.ToLower(os.Getenv("EMBED_PROVIDER"))
	if provider == "" {
		provider = "ollama"
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	openaiBase := os.Getenv("OPENAI_BASE_URL")
	if openaiBase == "" {
		openaiBase = "https://api.openai.com/v1"
	}

	return &QdrantMemory{
		qdrantURL:      qdrantURL,
		collectionName: collectionName,
		vectorSize:     vectorSize,
		distance:       "Cosine",
		ollamaURL:      ollamaURL,
		embedModel:     embedModel,
		embedProvider:  provider,
		embedAPIKey:    apiKey,
		openaiBaseURL:  openaiBase,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Store 将内容转换为向量后存储到 Qdrant
func (m *QdrantMemory) Store(content string, metadata map[string]string) error {
	vec, err := m.generateEmbedding(content)
	if err != nil {
		return fmt.Errorf("embedding生成失败: %w", err)
	}

	id := generateID()
	payload := map[string]interface{}{
		"content": content,
	}
	for k, v := range metadata {
		payload[k] = v
	}

	point := qdrantPoint{
		ID:      id,
		Vector:  vec,
		Payload: payload,
	}

	if err := m.upsertPoints([]qdrantPoint{point}); err != nil {
		return fmt.Errorf("存储到Qdrant失败: %w", err)
	}
	return nil
}

// Retrieve 使用查询文本的向量在 Qdrant 中搜索相似记忆
func (m *QdrantMemory) Retrieve(query string, limit int) ([]string, error) {
	vec, err := m.generateEmbedding(query)
	if err != nil {
		return nil, fmt.Errorf("embedding生成失败: %w", err)
	}

	results, err := m.searchPoints(vec, limit)
	if err != nil {
		return nil, fmt.Errorf("Qdrant搜索失败: %w", err)
	}

	var contents []string
	for _, r := range results {
		if content, ok := r.Payload["content"].(string); ok {
			contents = append(contents, content)
		}
	}
	return contents, nil
}

// Clear 删除集合并重建（清空所有记忆）
func (m *QdrantMemory) Clear() error {
	req, err := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/collections/%s", m.qdrantURL, m.collectionName), nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("清除集合失败 (%d): %s", resp.StatusCode, string(body))
	}

	m.mu.Lock()
	m.collectionInited = false
	m.mu.Unlock()
	return nil
}

// ---------- 内部方法 ----------

// ensureCollection 确保集合存在（自动创建）
func (m *QdrantMemory) ensureCollection() error {
	m.mu.RLock()
	if m.collectionInited {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查集合是否存在
	checkReq, _ := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/collections/%s", m.qdrantURL, m.collectionName), nil)
	resp, err := m.client.Do(checkReq)
	if err == nil && resp.StatusCode == http.StatusOK {
		m.collectionInited = true
		resp.Body.Close()
		return nil
	}
	if resp != nil {
		resp.Body.Close()
	}

	// 创建集合
	createBody := qdrantCreateCollection{
		Vectors: qdrantVectorParams{
			Size:     m.vectorSize,
			Distance: m.distance,
		},
	}
	jsonBody, _ := json.Marshal(createBody)
	putReq, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/collections/%s", m.qdrantURL, m.collectionName),
		bytes.NewReader(jsonBody))
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := m.client.Do(putReq)
	if err != nil {
		return fmt.Errorf("创建集合失败: %w", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("创建集合失败 (%d): %s", putResp.StatusCode, string(body))
	}

	m.collectionInited = true
	return nil
}

// upsertPoints 插入点（自动创建集合）
func (m *QdrantMemory) upsertPoints(points []qdrantPoint) error {
	if err := m.ensureCollection(); err != nil {
		return err
	}

	upsertBody := qdrantUpsertPoints{Points: points}
	jsonBody, _ := json.Marshal(upsertBody)
	url := fmt.Sprintf("%s/collections/%s/points", m.qdrantURL, m.collectionName)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upsert失败 (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// searchPoints 在 Qdrant 中进行向量搜索
func (m *QdrantMemory) searchPoints(vector []float32, limit int) ([]struct {
	ID      string                 `json:"id"`
	Payload map[string]interface{} `json:"payload"`
	Score   float32                `json:"score"`
}, error) {
	if err := m.ensureCollection(); err != nil {
		return nil, err
	}

	searchBody := qdrantSearchRequest{
		Vector:      vector,
		Limit:       limit,
		WithPayload: true,
	}
	jsonBody, _ := json.Marshal(searchBody)
	url := fmt.Sprintf("%s/collections/%s/points/search", m.qdrantURL, m.collectionName)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("搜索失败 (%d): %s", resp.StatusCode, string(body))
	}

	var searchResp qdrantSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, err
	}
	return searchResp.Result, nil
}

// generateEmbedding 根据配置的嵌入提供者生成向量
func (m *QdrantMemory) generateEmbedding(text string) ([]float32, error) {
	if len(text) > 2000 {
		text = text[:2000]
	}

	switch m.embedProvider {
	case "ollama":
		return m.ollamaEmbed(text)
	case "openai":
		return m.openaiEmbed(text)
	case "anthropic":
		return nil, fmt.Errorf("Anthropic 暂不支持文本嵌入功能，请使用 ollama 或 openai 作为嵌入提供者")
	default:
		return nil, fmt.Errorf("不支持的嵌入提供者: %s", m.embedProvider)
	}
}

func (m *QdrantMemory) ollamaEmbed(text string) ([]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model:  m.embedModel,
		Prompt: text,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := m.client.Post(
		fmt.Sprintf("%s/api/embeddings", m.ollamaURL),
		"application/json",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		return nil, fmt.Errorf("请求ollama嵌入接口失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama嵌入返回错误 (%d): %s", resp.StatusCode, string(body))
	}

	var embedResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, err
	}

	vec := make([]float32, len(embedResp.Embedding))
	for i, v := range embedResp.Embedding {
		vec[i] = float32(v)
	}
	return vec, nil
}

func (m *QdrantMemory) openaiEmbed(text string) ([]float32, error) {
	if m.embedAPIKey == "" {
		return nil, fmt.Errorf("OpenAI API 密钥未设置")
	}

	reqBody := openAIEmbedRequest{
		Input: text,
		Model: m.embedModel,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/embeddings", strings.TrimRight(m.openaiBaseURL, "/"))
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.embedAPIKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求OpenAI嵌入接口失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI嵌入返回错误 (%d): %s", resp.StatusCode, string(body))
	}

	var embedResp openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, err
	}
	if len(embedResp.Data) == 0 {
		return nil, fmt.Errorf("OpenAI 嵌入返回空数据")
	}

	vec := make([]float32, len(embedResp.Data[0].Embedding))
	for i, v := range embedResp.Data[0].Embedding {
		vec[i] = float32(v)
	}
	return vec, nil
}

// generateID 生成随机十六进制 ID
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("生成ID失败，使用回退方案: %v", err)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
