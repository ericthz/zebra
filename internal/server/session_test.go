package server

import (
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/provider"
)

func TestSessionCreateGetExpire(t *testing.T) {
	store := NewInMemoryStore(50 * time.Millisecond)
	defer store.Stop()

	sess, err := store.Create("alice", "tenant-a", "user", 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.Tenant != "tenant-a" {
		t.Fatal("会话元数据错误")
	}

	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("有效会话应可取到")
	}

	// 过期后不可取
	time.Sleep(80 * time.Millisecond)
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("过期会话不应可取到")
	}

	// 触达续期
	sess2, _ := store.Create("bob", "t", "user", 100*time.Millisecond)
	if !store.Touch(sess2.ID) {
		t.Fatal("触达应成功")
	}
}

func TestSessionIsolation(t *testing.T) {
	// 每个会话持有独立历史（A4 隔离）
	store := NewInMemoryStore(time.Minute)
	defer store.Stop()

	s1, _ := store.Create("a", "t1", "user", time.Minute)
	s2, _ := store.Create("a", "t1", "user", time.Minute)
	h1, h2 := s1.History(), s2.History()

	*h1 = append(*h1, provider.Message{Role: "user", Content: "hello"})
	if len(*h2) != 0 {
		t.Fatal("两个会话历史应互相隔离")
	}
}

// TestForgetUser 被遗忘权：删除用户全部会话。
func TestForgetUser(t *testing.T) {
	store := NewInMemoryStore(time.Minute)
	defer store.Stop()

	store.Create("alice", "t1", "user", time.Minute)
	store.Create("alice", "t1", "user", time.Minute)
	store.Create("bob", "t1", "user", time.Minute)

	ids := store.ForgetUser("alice")
	if len(ids) != 2 {
		t.Fatalf("应删除 alice 的 2 个会话，实际 %d", len(ids))
	}
	// bob 不受影响
	sess, _ := store.Create("bob", "t1", "user", time.Minute)
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("bob 的会话不应被误删")
	}
}
