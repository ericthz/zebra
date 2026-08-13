// Redis 长期记忆（P51）：memory.Memory 的 Redis 实现（关键词检索）。
//
// 背景：Qdrant 做向量语义检索；Redis 则提供"轻量、可水平扩展"的长期记忆
// （记录最近说过什么，关键词重叠打分），适合无向量库的部署。会话/任务已
// 迁 Redis（P28/P43），本实现把"长期记忆"也纳入 Redis 家族。
//
// 存储：key = zebra:mem:<tenant>，值为 JSON 数组（含内容/元数据/时间），
// 读写采用"整数组读改写"，每租户保留最近 max 条（默认 200）。
// 生产演化方向：改为 RPUSH/LRANGE 列表 + SCAN；检索升级为向量（如
// 用 Qdrant）；TTL 策略与遗忘机制（P22）联动。
package memory

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/ericthz/zebra/internal/redis"
)

const redisMemPrefix = "zebra:mem:"

// memEntry 一条 Redis 记忆条目。
type memEntry struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitempty"`
	Time    time.Time         `json:"time"`
}

// RedisMemory 基于 Redis 的长期记忆实现。
type RedisMemory struct {
	client *redis.Client
	key    string // zebra:mem:<tenant>
	max    int    // 每租户保留条数
}

// NewRedisMemory 构造（tenant 用于 A4 租户隔离）。
func NewRedisMemory(client *redis.Client, tenant string) *RedisMemory {
	return &RedisMemory{client: client, key: redisMemPrefix + tenant, max: 200}
}

// ForTenant 返回绑定到指定租户的实例（A4）。
func (m *RedisMemory) ForTenant(tenant string) Memory {
	return NewRedisMemory(m.client, tenant)
}

// Store 追加一条记忆（保留最近 max 条）。
func (m *RedisMemory) Store(ctx context.Context, content string, meta map[string]string) error {
	entries := m.load(ctx)
	entries = append(entries, memEntry{Content: content, Meta: meta, Time: time.Now()})
	if len(entries) > m.max {
		entries = entries[len(entries)-m.max:]
	}
	return m.save(ctx, entries)
}

// Retrieve 按关键词重叠打分返回最相关的 limit 条（无向量，关键词近似）。
func (m *RedisMemory) Retrieve(ctx context.Context, query string, limit int) ([]string, error) {
	entries := m.load(ctx)
	q := memTokens(query)
	scored := make([]struct {
		content string
		score   int
	}, 0, len(entries))
	for _, e := range entries {
		s := 0
		seen := map[string]bool{}
		for t := range memTokens(e.Content) {
			if !seen[t] && q[t] {
				seen[t] = true
				s++
			}
		}
		scored = append(scored, struct {
			content string
			score   int
		}{e.Content, s})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	out := make([]string, 0, limit)
	for _, s := range scored {
		if s.score == 0 {
			continue
		}
		out = append(out, s.content)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Clear 删除该租户全部记忆。
func (m *RedisMemory) Clear(ctx context.Context) error {
	_, err := m.client.Del(ctx, m.key)
	return err
}

func (m *RedisMemory) load(ctx context.Context) []memEntry {
	raw, ok, err := m.client.Get(ctx, m.key)
	if err != nil || !ok {
		return nil
	}
	var entries []memEntry
	if json.Unmarshal([]byte(raw), &entries) != nil {
		return nil
	}
	return entries
}

func (m *RedisMemory) save(ctx context.Context, entries []memEntry) error {
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return m.client.Set(ctx, m.key, string(raw), 0)
}

// memTokens 关键词切分：英文整词 + 中文逐字（与技能检索同策略）。
func memTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, field := range strings.Fields(strings.ToLower(s)) {
		ascii := true
		for _, r := range field {
			if r >= 0x4e00 && r <= 0x9fff {
				out[string(r)] = true
				ascii = false
			}
		}
		if ascii {
			out[field] = true
		}
	}
	return out
}

// 编译期断言：实现 Memory 与 TenantScoped。
var (
	_ Memory       = (*RedisMemory)(nil)
	_ TenantScoped = (*RedisMemory)(nil)
)
