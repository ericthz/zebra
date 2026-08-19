package task

import (
	"context"
	"sync"
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

// TestManagerPanicRecovered 验证：run panic 时任务落为失败、进程不崩（六2）。
func TestManagerPanicRecovered(t *testing.T) {
	store := NewInMemoryStore()
	run := func(_ context.Context, _ string, _ string, _ string, _ []byte) (string, []byte, error) {
		panic("LLM 边角输入触发异常")
	}
	m := NewManager(store, run, 1)
	m.Start()
	defer m.Stop()

	id, err := m.Submit("u1", "s1", "boom")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var task *Task
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if tk, ok := m.Get(id); ok && tk.Status == StatusFailed {
			task = tk
			break
		}
	}
	if task == nil {
		t.Fatal("panic 任务应被 recover 并落为失败")
	}
	if task.Error == "" || task.Progress != "任务异常终止" {
		t.Fatalf("失败信息未记录: %+v", task)
	}
}

// TestStoreNoSharedPointerRace 验证：execute 写任务与 Get/List 并发读无数据竞争
// （配合 -race 运行；防御性拷贝保证调用方拿不到内部共享指针）。
func TestStoreNoSharedPointerRace(t *testing.T) {
	store := NewInMemoryStore()
	run := func(_ context.Context, _ string, _ string, _ string, _ []byte) (string, []byte, error) {
		time.Sleep(10 * time.Millisecond)
		return "ok", []byte("cp"), nil
	}
	m := NewManager(store, run, 4)
	m.Start()
	defer m.Stop()

	ids := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		id, err := m.Submit("u1", "s1", "t")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	stop := make(chan struct{})
	var readerDone sync.WaitGroup
	readerDone.Add(1)
	go func() {
		defer readerDone.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			store.List("u1")
			for _, id := range ids {
				if tk, ok := store.Get(id); ok {
					_ = tk.Status
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		for _, id := range ids {
			if tk, ok := m.Get(id); !ok || (tk.Status != StatusDone && tk.Status != StatusFailed) {
				allDone = false
			}
		}
		if allDone {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 停止读取 goroutine
	close(stop)
	readerDone.Wait()
}

// TestManagerConcurrency 测试并发上限：提交 6 个任务，最大并发 2，验证全部完成且不串扰。
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

// TestSubmitQueueFullReturnsError P2-11：队列满时 Submit 立即返回 ErrQueueFull，
// 不得无限阻塞。执行函数阻塞，让并发槽+队列全部占满。
func TestSubmitQueueFullReturnsError(t *testing.T) {
	store := NewInMemoryStore()
	block := make(chan struct{})
	run := func(_ context.Context, _ string, _ string, _ string, _ []byte) (string, []byte, error) {
		<-block // 挂起所有 worker，占满并发槽
		return "ok", nil, nil
	}

	m := NewManager(store, run, 4)
	m.Start()
	defer m.Stop()

	// 远超容量（4 并发槽 + 64 队列）连续提交：队列满后必然出现 ErrQueueFull，
	// 且每个 Submit 都必须立即返回、不能阻塞。
	gotFull := false
	for i := 0; i < 200; i++ {
		done := make(chan error, 1)
		go func() {
			_, err := m.Submit("u1", "s1", "排队")
			done <- err
		}()
		select {
		case err := <-done:
			if err == ErrQueueFull {
				gotFull = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("第 %d 个提交阻塞了（P2-11 未修复）", i)
		}
	}
	if !gotFull {
		t.Fatal("队列应出现满载拒绝（200 > 4+64）")
	}
	close(block)
}
