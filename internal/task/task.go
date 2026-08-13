// Package task 异步长任务（P12）。
//
// 背景：对话 API 是同步的，但真实业务常有长耗时任务（生成季度报告、
// 批量处理文件）。同步等待会占满连接/拖垮体验，正确做法是【异步化】：
//
//	提交 → 后台执行 → 轮询进度 → 完成通知
//
// 本包实现（纯内存，单机够用；生产换 Redis/数据库）：
//   - Task：任务状态机（pending → running → done/failed）+ 进度 + 结果
//   - InMemoryStore：任务存储（并发安全）
//   - Manager：消费者队列（并发上限），后台执行 + 进度更新 + 完成 Webhook
//   - 检查点（checkpoint）：任务携带会话历史快照，可断点恢复
//
// 设计要点（避免循环依赖）：
//
//	Manager 不 import server/agent，而是通过注入的 run 函数执行任务；
//	server 层负责把"Agent 运行"包装成 run（加载检查点、跑任务、存新检查点）。
//
// 生产演化方向：
//   - 存储换 Redis/DB；执行换独立 worker 进程（Kafka 消息队列）
//   - 失败重试 + 死信队列；任务取消（context cancel）
//   - 任务调度（复用 P4 schedule：定时触发长任务）
package task

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// 任务状态。
const (
	StatusPending  = "pending"
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
)

// Task 一个异步任务。
type Task struct {
	ID         string    `json:"id"`
	User       string    `json:"user"`
	Session    string    `json:"session"`
	Prompt     string    `json:"prompt"`
	Status     string    `json:"status"`
	Progress   string    `json:"progress,omitempty"` // 人类可读的进度说明
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	Checkpoint []byte    `json:"-"` // 会话历史快照（检查点，不暴露给客户端）
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
}

// Store 任务存储接口（可替换 Redis/DB）。
type Store interface {
	Create(t *Task) error
	Get(id string) (*Task, bool)
	Update(t *Task) error
	List(user string) []*Task
}

// InMemoryStore 内存任务存储（并发安全）。
type InMemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewInMemoryStore 构造。
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{tasks: make(map[string]*Task)}
}

func (s *InMemoryStore) Create(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t
	return nil
}

func (s *InMemoryStore) Get(id string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	return t, ok
}

func (s *InMemoryStore) Update(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Updated = time.Now()
	s.tasks[t.ID] = t
	return nil
}

func (s *InMemoryStore) List(user string) []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Task
	for _, t := range s.tasks {
		if t.User == user {
			out = append(out, t)
		}
	}
	return out
}

// RunFunc 由 server 注入的任务执行函数（避免 task 依赖 agent/server）。
// 参数：用户/会话（隔离 + 绑定 Agent 用）+ 提示词 + 检查点（可为空）；
// 返回：结果文本 + 新的检查点 + 错误。
type RunFunc func(ctx context.Context, user, session, prompt string, checkpoint []byte) (result string, cpOut []byte, err error)

// NotifyFunc 完成/失败时的通知回调（复用 P4 Webhook）。
type NotifyFunc func(t *Task)

// Manager 异步任务管理器（消费者队列）。
type Manager struct {
	store         Store
	run           RunFunc
	notify        NotifyFunc
	maxConcurrent int
	sem           chan struct{}
	queue         chan *Task
	stop          chan struct{}
	wg            sync.WaitGroup
	nextID        int64
	idMu          sync.Mutex
}

// NewManager 构造。
func NewManager(store Store, run RunFunc, maxConcurrent int) *Manager {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	m := &Manager{
		store: store, run: run, maxConcurrent: maxConcurrent,
		sem:   make(chan struct{}, maxConcurrent),
		queue: make(chan *Task, 64),
		stop:  make(chan struct{}),
	}
	return m
}

// SetNotify 设置完成通知回调。
func (m *Manager) SetNotify(fn NotifyFunc) { m.notify = fn }

// Start 启动消费循环（常驻，阻塞前启动 goroutine）。
func (m *Manager) Start() {
	m.wg.Add(1)
	go m.loop()
}

// loop 消费队列，用信号量限制并发。
func (m *Manager) loop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case t := <-m.queue:
			m.sem <- struct{}{} // 占用并发槽
			m.wg.Add(1)
			go func(t *Task) {
				defer m.wg.Done()
				defer func() { <-m.sem }() // 释放并发槽
				m.execute(t)
			}(t)
		}
	}
}

// Stop 优雅停止（等待在途任务完成）。
func (m *Manager) Stop() {
	close(m.stop)
	m.wg.Wait()
}

// Submit 提交任务，返回任务 ID。
func (m *Manager) Submit(user, session, prompt string) (string, error) {
	t := &Task{
		ID: m.nextTaskID(), User: user, Session: session, Prompt: prompt,
		Status: StatusPending, Created: time.Now(), Updated: time.Now(),
	}
	if err := m.store.Create(t); err != nil {
		return "", err
	}
	m.queue <- t
	return t.ID, nil
}

// Get 查询任务。
func (m *Manager) Get(id string) (*Task, bool) { return m.store.Get(id) }

// List 列出某用户的全部任务。
func (m *Manager) List(user string) []*Task { return m.store.List(user) }

// execute 执行一个任务并更新状态/进度/结果/检查点。
func (m *Manager) execute(t *Task) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// running
	t.Status = StatusRunning
	t.Progress = "任务开始执行…"
	m.store.Update(t)

	// 执行（注入的 run 内部驱动进度；此处不做细分，保持简单）
	result, cpOut, err := m.run(ctx, t.User, t.Session, t.Prompt, t.Checkpoint)
	if err != nil {
		t.Status = StatusFailed
		t.Error = err.Error()
		m.store.Update(t)
	} else {
		t.Status = StatusDone
		t.Result = result
		t.Checkpoint = cpOut // 新检查点（供后续恢复/继续）
		t.Progress = "任务完成"
		m.store.Update(t)
	}

	// 完成通知（异步，不阻塞）
	if m.notify != nil {
		go m.notify(t)
	}
}

func (m *Manager) nextTaskID() string {
	m.idMu.Lock()
	defer m.idMu.Unlock()
	m.nextID++
	return fmt.Sprintf("task-%d-%d", time.Now().Unix(), m.nextID)
}
