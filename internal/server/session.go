// 会话管理：多会话并发、TTL 过期、租户/角色隔离。
//
// 当前为进程内内存实现（单机部署足够）；生产替换为 Redis/数据库，
// 只需实现 SessionStore 接口。
package server

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/ericthz/zebra/internal/provider"
)

// Session 一次会话：持有与 Agent 共享的历史，绑定租户/用户/角色（隔离）。
type Session struct {
	ID      string
	Tenant  string
	User    string
	Role    string
	Created time.Time
	Expires time.Time

	mu      sync.RWMutex
	history []provider.Message

	// runMu 串行化同一会话的 Agent 执行：历史切片由 Agent 无锁就地
	// 追加/读取，同一会话并发两个请求会数据竞争（多轮上下文撕裂）。
	// 会话的对话本就是顺序的，串行执行语义正确且代价极小。
	//
	// 注意：Redis 会话存储每次 Get 都重建 Session，锁必须跨实例共享，
	// 故用指针——由存储层按会话 ID 维护统一互斥体（见 RedisSessionStore）。
	runMu *sync.Mutex
}

// Expired 是否过期。
func (s *Session) Expired() bool { return time.Now().After(s.Expires) }

// History 返回历史切片指针（供 Agent.Bind 共享）。
func (s *Session) History() *[]provider.Message { return &s.history }

// SessionStore 会话存储接口（可插拔 Redis/DB 实现）。
type SessionStore interface {
	Get(id string) (*Session, bool)
	Create(user, tenant, role string, ttl time.Duration) (*Session, error)
	Delete(id string)
	Touch(id string) bool            // 续期
	ForgetUser(user string) []string // 被遗忘权：删该用户全部会话，返回被删 ID
}

// HistoryPersister 可选接口：会话存储需要"历史写回"时实现（Redis）。
// Agent 在对话中通过 History() 指针就地修改历史，内存实现天然同步；
// Redis 等分布式存储必须在请求结束前把最新历史显式写回，否则多轮丢失。
type HistoryPersister interface {
	Save(s *Session) error
}

// InMemoryStore 内存会话存储：懒创建 + 定期清理过期会话。
type InMemoryStore struct {
	mu    sync.RWMutex
	items map[string]*Session
	ttl   time.Duration
	stop  chan struct{}
}

// NewInMemoryStore 构造，并启动后台过期清理（过期）。
func NewInMemoryStore(ttl time.Duration) *InMemoryStore {
	s := &InMemoryStore{items: make(map[string]*Session), ttl: ttl, stop: make(chan struct{})}
	go s.cleanupLoop()
	return s
}

// Stop 停止后台清理（优雅停机时调用）。
func (s *InMemoryStore) Stop() { close(s.stop) }

func (s *InMemoryStore) cleanupLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.cleanupExpired()
		}
	}
}

// cleanupExpired 删除过期会话（C-1：与 Delete/ForgetUser 同保证）。
// 先快照过期会话，逐个取 runMu 后再删——防止在飞对话（持锁执行 rememberTurn/
// Profile.Learn/persistHistory）被清理 tick 误删：否则该轮写回落到已脱离
// map 的 *Session，用户整轮对话丢失。锁序与 chat 路径一致（runMu→map）。
func (s *InMemoryStore) cleanupExpired() {
	s.mu.RLock()
	var expired []*Session
	for _, sess := range s.items {
		if sess.Expired() {
			expired = append(expired, sess)
		}
	}
	s.mu.RUnlock()

	for _, sess := range expired {
		sess.runMu.Lock() // 等待该会话所有在飞对话结束
		s.mu.Lock()
		if s.items[sess.ID] == sess { // 期间可能已被并发删除/续期
			delete(s.items, sess.ID)
		}
		s.mu.Unlock()
		sess.runMu.Unlock()
	}
}

// Get 读取会话。
func (s *InMemoryStore) Get(id string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.items[id]
	if ok && sess.Expired() {
		return nil, false
	}
	return sess, ok
}

// Create 创建会话（返回新 ID）。
func (s *InMemoryStore) Create(user, tenant, role string, ttl time.Duration) (*Session, error) {
	if ttl <= 0 {
		ttl = s.ttl
	}
	sess := &Session{
		ID: newID(), Tenant: tenant, User: user, Role: role,
		Created: time.Now(), Expires: time.Now().Add(ttl),
		runMu: &sync.Mutex{},
	}
	s.mu.Lock()
	s.items[sess.ID] = sess
	s.mu.Unlock()
	return sess, nil
}

// Delete 删除会话。删除前先获取该会话的 runMu：保证删除严格
// 发生在所有在飞对话（持锁执行 rememberTurn/Profile.Learn/persistHistory）
// 完成之后，否则 in-flight 的写回会让已删会话"复活"。
func (s *InMemoryStore) Delete(id string) {
	s.mu.RLock()
	sess, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return
	}
	sess.runMu.Lock()
	defer sess.runMu.Unlock()
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
}

// ForgetUser 被遗忘权：删除某用户全部会话，返回被删会话 ID。
// 先 RLock 快照该用户的会话（避免持 map 锁去拿 runMu 造成锁序反转：
// chat 路径是 runMu→map，此处必须也是 runMu→map），逐个取 runMu 后再删。
func (s *InMemoryStore) ForgetUser(user string) []string {
	s.mu.RLock()
	var targets []*Session
	for _, sess := range s.items {
		if sess.User == user {
			targets = append(targets, sess)
		}
	}
	s.mu.RUnlock()

	var ids []string
	for _, sess := range targets {
		sess.runMu.Lock() // 等待该会话所有在飞对话结束（写历史/记忆/画像）
		s.mu.Lock()
		if s.items[sess.ID] == sess { // 仍存在才删（期间可能已被并发删除）
			delete(s.items, sess.ID)
			ids = append(ids, sess.ID)
		}
		s.mu.Unlock()
		sess.runMu.Unlock()
	}
	return ids
}

// Touch 续期会话（每次请求刷新 TTL）。
func (s *InMemoryStore) Touch(id string) bool {
	s.mu.RLock()
	sess, ok := s.items[id]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	s.mu.Lock()
	sess.Expires = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return true
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}
