package schedule

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestEvery 验证周期任务按间隔触发。
func TestEvery(t *testing.T) {
	s := NewScheduler()
	var n int32
	s.Every("tick", 20*time.Millisecond, func(context.Context) {
		atomic.AddInt32(&n, 1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(65 * time.Millisecond) // 期望 1(立即) + ~3 次间隔
	cancel()
	s.Stop()

	got := atomic.LoadInt32(&n)
	if got < 2 {
		t.Fatalf("周期任务应至少触发 2 次，实际 %d", got)
	}
}

// TestOnce 验证一次性任务在指定时刻触发。
func TestOnce(t *testing.T) {
	s := NewScheduler()
	var fired atomic.Bool
	s.Once("once", time.Now().Add(20*time.Millisecond), func(context.Context) {
		fired.Store(true)
	})

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(60 * time.Millisecond)
	cancel()
	s.Stop()

	if !fired.Load() {
		t.Fatal("一次性任务应已触发")
	}
}

// TestPanicIsolated 验证：任务 panic 不拖垮调度器。
func TestPanicIsolated(t *testing.T) {
	s := NewScheduler()
	var n atomic.Int32
	s.Every("panic-job", 15*time.Millisecond, func(context.Context) {
		if n.Add(1) == 1 {
			panic("boom") // 第一次执行即 panic
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	s.Stop()
	if n.Load() < 2 {
		t.Fatalf("panic 后任务应继续执行，实际 %d 次", n.Load())
	}
}
