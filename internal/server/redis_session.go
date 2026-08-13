// Redis 会话存储（P28 水平扩展）：SessionStore 的分布式实现。
//
// 价值：内存会话（InMemoryStore）把状态绑在单机进程上——多副本部署时
// 用户请求打到别的副本就"会话不存在"。换 Redis 后任意副本都能读写同一份
// 会话（配合前面手写的 RESP 客户端，仍零第三方依赖）。
//
// 设计：
//   - key：zebra:sess:<id>，值为 JSON（含用户/租户/角色/过期时间/历史）
//   - TTL 原生由 Redis 负责（SET EX / EXPIRE），无需后台清理协程
//   - 历史写回：实现 HistoryPersister 接口，Agent 修改历史后由 server 调 Save
//
// 生产演化方向：历史分块/游标分页（大会话）、SCAN 替代 KEYS 做
// ForgetUser、连接池复用、哨兵/集群故障转移。
package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/redis"
)

const redisSessPrefix = "zebra:sess:"

// sessionDTO 会话的持久化形态（Session 的未导出字段无法直接 JSON）。
type sessionDTO struct {
	ID      string
	Tenant  string
	User    string
	Role    string
	Created time.Time
	Expires time.Time
	History []provider.Message
}

// RedisSessionStore SessionStore 的 Redis 实现。
type RedisSessionStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisSessionStore 构造；ttl<=0 默认 30 分钟。
func NewRedisSessionStore(client *redis.Client, ttl time.Duration) *RedisSessionStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &RedisSessionStore{client: client, ttl: ttl}
}

// Get 从 Redis 读取会话；不存在/损坏/过期返回 false。
func (s *RedisSessionStore) Get(id string) (*Session, bool) {
	raw, ok, err := s.client.Get(context.Background(), redisSessPrefix+id)
	if err != nil || !ok {
		return nil, false
	}
	var dto sessionDTO
	if err := json.Unmarshal([]byte(raw), &dto); err != nil {
		return nil, false
	}
	sess := &Session{
		ID: dto.ID, Tenant: dto.Tenant, User: dto.User, Role: dto.Role,
		Created: dto.Created, Expires: dto.Expires, history: dto.History,
	}
	// 过期判断以 Redis TTL 为准：Touch 续期只改 Redis 里的 TTL，本地
	// Expires 字段会滞后，因此这里不做本地 Expired() 校验（避免误删
	// "Redis 里还活着但本地时间戳过期"的会话）。GET 取不到即视为过期。
	return sess, true
}

// Create 创建会话并立即落 Redis（SET EX ttl）。
func (s *RedisSessionStore) Create(user, tenant, role string, ttl time.Duration) (*Session, error) {
	if ttl <= 0 {
		ttl = s.ttl
	}
	sess := &Session{
		ID: newID(), Tenant: tenant, User: user, Role: role,
		Created: time.Now(), Expires: time.Now().Add(ttl),
	}
	if err := s.Save(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// Save 把会话（含最新历史）写回 Redis（HistoryPersister 接口实现）。
func (s *RedisSessionStore) Save(sess *Session) error {
	dto := sessionDTO{
		ID: sess.ID, Tenant: sess.Tenant, User: sess.User, Role: sess.Role,
		Created: sess.Created, Expires: sess.Expires,
		History: *sess.History(),
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		return err
	}
	ttl := time.Until(sess.Expires)
	if ttl <= 0 {
		ttl = s.ttl
	}
	return s.client.Set(context.Background(), redisSessPrefix+sess.ID, string(raw), ttl)
}

// Delete 删除会话。
func (s *RedisSessionStore) Delete(id string) {
	_, _ = s.client.Del(context.Background(), redisSessPrefix+id)
}

// Touch 续期；key 不存在返回 false（与内存实现语义一致）。
func (s *RedisSessionStore) Touch(id string) bool {
	ok, err := s.client.Expire(context.Background(), redisSessPrefix+id, s.ttl)
	return err == nil && ok
}

// ForgetUser 被遗忘权：KEYS 扫描全部会话，删除属于该用户的（返回被删 ID）。
// 生产演化方向：SCAN 分页 + 按用户维护会话索引，避免全量扫描。
func (s *RedisSessionStore) ForgetUser(user string) []string {
	ctx := context.Background()
	keys, err := s.client.Keys(ctx, redisSessPrefix+"*")
	if err != nil {
		return nil
	}
	var deleted []string
	for _, k := range keys {
		raw, ok, err := s.client.Get(ctx, k)
		if err != nil || !ok {
			continue
		}
		var dto sessionDTO
		if json.Unmarshal([]byte(raw), &dto) != nil {
			continue
		}
		if dto.User == user {
			_, _ = s.client.Del(ctx, k)
			deleted = append(deleted, dto.ID)
		}
	}
	return deleted
}

// 编译期断言：RedisSessionStore 实现 SessionStore 与 HistoryPersister。
var (
	_ SessionStore     = (*RedisSessionStore)(nil)
	_ HistoryPersister = (*RedisSessionStore)(nil)
)
