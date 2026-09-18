package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/redis"
	"github.com/ericthz/zebra/internal/redistest"
)

func TestRedisMemoryStoreRetrieve(t *testing.T) {
	srv := redistest.New(t)
	m := NewRedisMemory(&redis.Client{Addr: srv.Addr(), Timeout: 2 * time.Second}, "tenant-a")
	ctx := context.Background()

	m.Store(ctx, "用户喜欢火锅", map[string]string{"type": "conversation"})
	m.Store(ctx, "今天北京天气晴", nil)

	// 关键词命中：火锅
	hits, err := m.Retrieve(ctx, "", "火锅", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !contains(hits, "火锅") {
		t.Fatalf("关键词检索应命中火锅: %v", hits)
	}
	// 不相关查询 → 空
	if hits, _ := m.Retrieve(ctx, "", "量子物理", 5); len(hits) != 0 {
		t.Fatalf("不相关查询应为空: %v", hits)
	}
}

// TestRedisMemoryUserIsolation：检索必须按 user 过滤，不能跨用户泄露。
func TestRedisMemoryUserIsolation(t *testing.T) {
	srv := redistest.New(t)
	m := NewRedisMemory(&redis.Client{Addr: srv.Addr(), Timeout: 2 * time.Second}, "tenant-u")
	ctx := context.Background()

	m.Store(ctx, "alice 的火锅偏好", map[string]string{"user": "alice"})
	m.Store(ctx, "bob 的火锅偏好", map[string]string{"user": "bob"})

	// alice 只应看到 alice 的记忆
	ha, _ := m.Retrieve(ctx, "alice", "火锅", 5)
	if len(ha) != 1 || !strings.Contains(ha[0], "alice") {
		t.Fatalf("alice 不应看到 bob 的记忆: %v", ha)
	}
	// bob 只应看到 bob 的记忆
	hb, _ := m.Retrieve(ctx, "bob", "火锅", 5)
	if len(hb) != 1 || !strings.Contains(hb[0], "bob") {
		t.Fatalf("bob 不应看到 alice 的记忆: %v", hb)
	}
	// 未带 user 过滤则两者都返回（兼容内部调用）
	hall, _ := m.Retrieve(ctx, "", "火锅", 5)
	if len(hall) != 2 {
		t.Fatalf("空 user 应返回全部: %v", hall)
	}
}

func TestRedisMemoryMaxTrimAndClear(t *testing.T) {
	srv := redistest.New(t)
	m := NewRedisMemory(&redis.Client{Addr: srv.Addr(), Timeout: 2 * time.Second}, "tenant-b")
	m.max = 3
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		m.Store(ctx, "记录"+string(rune('0'+i)), nil)
	}
	// 只保留最近 3 条（记录2/3/4）
	hits, _ := m.Retrieve(ctx, "", "记录", 10)
	if len(hits) != 3 || contains(hits, "记录0") {
		t.Fatalf("应裁剪为最近 3 条: %v", hits)
	}
	if err := m.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if hits, _ := m.Retrieve(ctx, "", "记录", 10); len(hits) != 0 {
		t.Fatalf("Clear 后应为空: %v", hits)
	}
}

func TestRedisMemoryTenantIsolation(t *testing.T) {
	srv := redistest.New(t)
	client := &redis.Client{Addr: srv.Addr(), Timeout: 2 * time.Second}
	ctx := context.Background()
	a := NewRedisMemory(client, "ta")
	b := a.ForTenant("tb").(*RedisMemory)
	a.Store(ctx, "甲的记忆", nil)
	b.Store(ctx, "乙的记忆", nil)

	ha, _ := a.Retrieve(ctx, "", "甲", 5)
	hb, _ := b.Retrieve(ctx, "", "甲", 5)
	if len(ha) != 1 || len(hb) != 0 {
		t.Fatalf("租户应隔离: a=%v b=%v", ha, hb)
	}
}

func TestRedisMemoryForgetUser(t *testing.T) {
	srv := redistest.New(t)
	m := NewRedisMemory(&redis.Client{Addr: srv.Addr(), Timeout: 2 * time.Second}, "tenant-c")
	ctx := context.Background()

	m.Store(ctx, "alice 的火锅偏好", map[string]string{"user": "alice"})
	m.Store(ctx, "bob 的火锅偏好", map[string]string{"user": "bob"})

	if err := m.ForgetUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	// alice 的记忆应消失，bob 的应保留
	if hits, _ := m.Retrieve(ctx, "", "火锅", 5); len(hits) != 1 || !contains(hits, "bob") {
		t.Fatalf("只应删 alice，保留 bob: %v", hits)
	}
	// 再次删除不存在的用户应无副作用
	if err := m.ForgetUser(ctx, "nobody"); err != nil {
		t.Fatal(err)
	}
}

func TestSetupManagerRedis(t *testing.T) {
	// 可用 → 启用
	srv := redistest.New(t)
	mem, ok := SetupManagerRedis(&redis.Client{Addr: srv.Addr(), Timeout: 2 * time.Second}, discardLogger())
	if !ok || mem == nil || mem.Long == nil {
		t.Fatalf("Redis 可用应启用长期记忆: %v %v", mem, ok)
	}
	// 不可达 → 降级
	mem2, ok2 := SetupManagerRedis(&redis.Client{Addr: "127.0.0.1:1", Timeout: 2 * time.Second}, discardLogger())
	if ok2 || mem2.Long != nil {
		t.Fatalf("Redis 不可达应降级: %v %v", mem2, ok2)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.Contains(v, s) {
			return true
		}
	}
	return false
}
