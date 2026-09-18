// Package schedule 定时调度能力（主动出站）。
//
// 背景：Agent 此前只会"响应式"回答。成熟 Agent 需要【主动触发】：
//   - 定时任务：每天 9 点自动汇总、每小时巡检
//   - 一次性任务：延迟 N 秒后执行
//
// 本包实现最小调度器：周期任务(Every) + 一次性任务(Once)，
// 后台 goroutine 执行，支持优雅停止（配合 context）。
//
// 生产演化方向：
//   - 用 cron 表达式（quarz/cron 库）支持复杂调度
//   - 调度器多实例部署时用分布式锁/Redis 防重复执行
//   - 任务失败重试 + 死信队列（复用 notify）
package schedule

import (
	"context"
	"sync"
	"time"
)

// Scheduler 最小调度器（并发安全）。
type Scheduler struct {
	mu   sync.Mutex
	jobs map[string]*job
	done chan struct{} // 关闭即停止所有任务
	wg   sync.WaitGroup
}

type job struct {
	id       string
	interval time.Duration // >0 = 周期任务
	runAt    time.Time     // 一次性任务触发时刻
	fn       func(ctx context.Context)
}

// NewScheduler 构造。
func NewScheduler() *Scheduler {
	return &Scheduler{jobs: make(map[string]*job), done: make(chan struct{})}
}

// Every 注册周期任务（interval 为触发间隔）。
func (s *Scheduler) Every(id string, interval time.Duration, fn func(ctx context.Context)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = &job{id: id, interval: interval, fn: fn}
}

// Once 注册一次性任务（at 时刻触发一次）。
func (s *Scheduler) Once(id string, at time.Time, fn func(ctx context.Context)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = &job{id: id, runAt: at, fn: fn}
}

// Start 启动全部已注册任务（阻塞到 ctx 取消或 Stop 调用）。
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		s.wg.Add(1)
		go s.run(ctx, j)
	}
}

// run 单个任务的执行循环。
func (s *Scheduler) run(ctx context.Context, j *job) {
	defer s.wg.Done()
	if j.interval > 0 {
		// 周期任务：立即执行一次，之后按间隔循环
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		s.safeRun(ctx, j)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				return
			case <-ticker.C:
				s.safeRun(ctx, j)
			}
		}
	}
	// 一次性任务：等待触发时刻
	timer := time.NewTimer(time.Until(j.runAt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-s.done:
		return
	case <-timer.C:
		s.safeRun(ctx, j)
	}
}

// safeRun 执行任务并兜住 panic（单个任务崩溃不影响调度器）。
func (s *Scheduler) safeRun(ctx context.Context, j *job) {
	defer func() { recover() }() // 任务 panic 时记录日志并继续
	j.fn(ctx)
}

// Stop 停止所有任务并等待退出（优雅停机）。
func (s *Scheduler) Stop() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	s.wg.Wait()
}
