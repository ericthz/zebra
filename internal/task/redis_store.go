// Redis 任务存储（P43）：task.Store 的 Redis 实现，支撑水平扩展。
//
// 背景：InMemoryStore 把任务绑在单机进程，多副本部署时任务互相不可见。
// 本实现把任务以 JSON 存于 Redis（zebra:task:<id>，TTL 兜底清理），
// 任意副本都能读写同一份任务，配合 Manager 消费即"分布式任务队列骨架"。
//
// 说明：Task.Checkpoint（会话历史快照）字段带 json:"-"，不随任务持久化
// （检查点属运行态数据；生产演化方向：单独存 blob，按任务 ID 关联）。
package task

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ericthz/zebra/internal/redis"
)

const redisTaskPrefix = "zebra:task:"

// RedisTaskStore 基于 Redis 的任务存储。
type RedisTaskStore struct {
	client *redis.Client
}

// NewRedisTaskStore 构造。
func NewRedisTaskStore(client *redis.Client) *RedisTaskStore {
	return &RedisTaskStore{client: client}
}

// Create 写入任务（SET + TTL 兜底清理，避免孤儿任务堆积）。
func (s *RedisTaskStore) Create(t *Task) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.client.Set(context.Background(), redisTaskPrefix+t.ID, string(raw), 24*time.Hour)
}

// Get 读取任务。
func (s *RedisTaskStore) Get(id string) (*Task, bool) {
	raw, ok, err := s.client.Get(context.Background(), redisTaskPrefix+id)
	if err != nil || !ok {
		return nil, false
	}
	var t Task
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, false
	}
	return &t, true
}

// Update 全量覆盖写（并刷新更新时间）。
func (s *RedisTaskStore) Update(t *Task) error {
	t.Updated = time.Now()
	return s.Create(t)
}

// List 返回某用户的全部任务（KEYS 扫描 + 过滤，生产演化方向：SCAN）。
func (s *RedisTaskStore) List(user string) []*Task {
	ctx := context.Background()
	keys, err := s.client.Keys(ctx, redisTaskPrefix+"*")
	if err != nil {
		return nil
	}
	var out []*Task
	for _, k := range keys {
		raw, ok, err := s.client.Get(ctx, k)
		if err != nil || !ok {
			continue
		}
		var t Task
		if json.Unmarshal([]byte(raw), &t) != nil {
			continue
		}
		if t.User == user {
			out = append(out, &t)
		}
	}
	return out
}

// 编译期断言：实现 Store 接口。
var _ Store = (*RedisTaskStore)(nil)
