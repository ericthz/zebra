package server

import (
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/redis"
	"github.com/ericthz/zebra/internal/redistest"
)

func TestRedisSessionStore(t *testing.T) {
	fs := redistest.New(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.Addr(), Timeout: 2 * time.Second}, 30*time.Minute)

	// 创建 → 读取
	sess, err := store.Create("alice", "default", "user", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.User != "alice" {
		t.Fatalf("会话元数据错误: %+v", sess)
	}
	got, ok := store.Get(sess.ID)
	if !ok || got.User != "alice" {
		t.Fatalf("应从 Redis 读到会话: %+v %v", got, ok)
	}

	// 历史写回（HistoryPersister）：修改后 Save → 再读仍保留（多轮不丢）
	*got.History() = append(*got.History(), provider.Message{Role: "user", Content: "第一轮"})
	if err := store.Save(got); err != nil {
		t.Fatal(err)
	}
	got2, ok := store.Get(sess.ID)
	if !ok || len(*got2.History()) != 1 || (*got2.History())[0].Content != "第一轮" {
		t.Fatalf("历史写回失败: %+v %v", got2, ok)
	}

	// Touch 续期：60ms 会过期，Touch 后延长到 30min
	short, _ := store.Create("bob", "default", "user", 60*time.Millisecond)
	if ok := store.Touch(short.ID); !ok {
		t.Fatal("Touch 应成功")
	}
	time.Sleep(120 * time.Millisecond)
	if _, ok := store.Get(short.ID); !ok {
		t.Fatal("Touch 续期后不应过期")
	}

	// 不存在的会话 Touch → false
	if store.Touch("ghost-session") {
		t.Fatal("不存在会话 Touch 应返回 false")
	}
}

func TestRedisSessionForgetUser(t *testing.T) {
	fs := redistest.New(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.Addr()}, time.Minute)

	a1, _ := store.Create("alice", "default", "user", time.Minute)
	a2, _ := store.Create("alice", "default", "user", time.Minute)
	b1, _ := store.Create("bob", "default", "user", time.Minute)

	deleted := store.ForgetUser("alice")
	if len(deleted) != 2 {
		t.Fatalf("应删除 alice 的 2 个会话，实际 %d: %v", len(deleted), deleted)
	}
	if _, ok := store.Get(a1.ID); ok {
		t.Fatal("alice 会话应被删除")
	}
	if _, ok := store.Get(a2.ID); ok {
		t.Fatal("alice 会话应被删除")
	}
	if _, ok := store.Get(b1.ID); !ok {
		t.Fatal("bob 会话不应被误删")
	}
}
