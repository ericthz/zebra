package task

import (
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/redis"
	"github.com/ericthz/zebra/internal/redistest"
)

func TestRedisTaskStore(t *testing.T) {
	srv := redistest.New(t)
	store := NewRedisTaskStore(&redis.Client{Addr: srv.Addr(), Timeout: 2 * time.Second})

	// 创建 → 读取（字段完整保留）
	tk := &Task{
		ID: "t1", User: "alice", Session: "s1", Prompt: "写周报",
		Status: StatusPending, Created: time.Now(),
	}
	if err := store.Create(tk); err != nil {
		t.Fatal(err)
	}
	got, ok := store.Get("t1")
	if !ok || got.User != "alice" || got.Prompt != "写周报" || got.Status != StatusPending {
		t.Fatalf("创建/读取异常: %+v %v", got, ok)
	}

	// 更新：结果 + 状态变化可见
	tk.Status = StatusDone
	tk.Result = "周报完成"
	if err := store.Update(tk); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get("t1")
	if got.Status != StatusDone || got.Result != "周报完成" || got.Updated.IsZero() {
		t.Fatalf("更新异常: %+v", got)
	}

	// 不存在的任务
	if _, ok := store.Get("ghost"); ok {
		t.Fatal("不存在任务应返回 false")
	}

	// 用户过滤（隔离）
	other := &Task{ID: "t2", User: "bob", Prompt: "x"}
	store.Create(other)
	alice := store.List("alice")
	if len(alice) != 1 || alice[0].ID != "t1" {
		t.Fatalf("alice 应只看到自己的任务: %+v", alice)
	}
}
