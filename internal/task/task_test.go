package task

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// 测试用执行函数：模拟慢任务并返回结果+检查点。
func TestManagerLifecycle(t *testing.T) {
	store := NewInMemoryStore()
	var executed int32
	run := func(_ context.Context, _ string, _ string, prompt string, cp []byte) (string, []byte, error) {
		atomic.AddInt32(&executed, 1)
		time.Sleep(50 * time.Millisecond) // 模拟耗时
		// 检查点：简单回显 prompt，模拟历史快照
		return "结果:" + prompt, []byte("cp:" + prompt), nil
	}

	m := NewManager(store, run, 2)
	m.Start()
	defer m.Stop()

	id, err := m.Submit("u1", "s1", "写报告")
	if err != nil {
		t.Fatal(err)
	}

	// 轮询直到完成（异步）
	deadline := time.Now().Add(2 * time.Second)
	var task *Task
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if t, ok := m.Get(id); ok && t.Status != StatusPending && t.Status != StatusRunning {
			task = t
			break
		}
	}
	if task == nil {
		t.Fatal("任务未在期限内完成")
	}
	if task.Status != StatusDone || task.Result != "结果:写报告" {
		t.Fatalf("任务状态/结果错误: %+v", task)
	}
	if string(task.Checkpoint) != "cp:写报告" {
		t.Fatalf("检查点未保存: %q", task.Checkpoint)
	}
	if atomic.LoadInt32(&executed) != 1 {
		t.Fatalf("应执行 1 次，实际 %d", executed)
	}
}

// 测试失败路径 + 通知回调。
func TestManagerFailureAndNotify(t *testing.T) {
	store := NewInMemoryStore()
	run := func(_ context.Context, _ string, _ string, _ string, _ []byte) (string, []byte, error) {
		return "", nil, context.Canceled
	}
	m := NewManager(store, run, 2)
	var notified atomic.Int32
	m.SetNotify(func(*Task) { notified.Add(1) })
	m.Start()
	defer m.Stop()

	id, _ := m.Submit("u1", "s1", "x")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if tk, ok := m.Get(id); ok && (tk.Status == StatusDone || tk.Status == StatusFailed) {
			if tk.Status != StatusFailed {
				t.Fatalf("应失败，实际 %s", tk.Status)
			}
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	if notified.Load() == 0 {
		t.Fatal("失败任务应触发完成通知")
	}
}

// 测试并发上限：提交 6 个任务，最大并发 2，验证全部完成且不串扰。
func TestManagerConcurrency(t *testing.T) {
	store := NewInMemoryStore()
	var running, maxRunning, done int32
	run := func(_ context.Context, _ string, _ string, _ string, _ []byte) (string, []byte, error) {
		n := atomic.AddInt32(&running, 1)
		for {
			m := atomic.LoadInt32(&maxRunning)
			if n <= m || atomic.CompareAndSwapInt32(&maxRunning, m, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&running, -1)
		atomic.AddInt32(&done, 1)
		return "ok", nil, nil
	}

	m := NewManager(store, run, 2)
	m.Start()
	defer m.Stop()

	for i := 0; i < 6; i++ {
		if _, err := m.Submit("u1", "s1", "t"); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if atomic.LoadInt32(&done) == 6 {
			break
		}
	}
	if atomic.LoadInt32(&done) != 6 {
		t.Fatalf("应完成 6 个任务，实际 %d", done)
	}
	if atomic.LoadInt32(&maxRunning) > 2 {
		t.Fatalf("并发应 ≤2，实际 %d", maxRunning)
	}
}
