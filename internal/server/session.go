// A2 会话管理：多会话并发、TTL 过期、租户/角色隔离。
//
// 当前为进程内内存实现（单机 demo 足够）；生产替换为 Redis/数据库，
// 只需实现 SessionStore 接口。
package server

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/ericthz/zebra/internal/provider"
)

// Session 一次会话：持有与 Agent 共享的历史，绑定租户/用户/角色（A4 隔离）。
type Session struct {
	ID      string
	Tenant  string
	User    string
	Role    string
	Created time.Time
	Expires time.Time

	mu      sync.RWMutex
	history []provider.Message
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
	ForgetUser(user string) []string // P6 被遗忘权：删该用户全部会话，返回被删 ID
}

// InMemoryStore 内存会话存储：懒创建 + 定期清理过期会话。
type InMemoryStore struct {
	mu    sync.RWMutex
	items map[string]*Session
	ttl   time.Duration
	stop  chan struct{}
}

// NewInMemoryStore 构造，并启动后台过期清理（A2 过期）。
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
			s.mu.Lock()
			for id, sess := range s.items {
				if sess.Expired() {
					delete(s.items, id)
				}
			}
			s.mu.Unlock()
		}
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
	}
	s.mu.Lock()
	s.items[sess.ID] = sess
	s.mu.Unlock()
	return sess, nil
}

// Delete 删除会话。
func (s *InMemoryStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
}

// ForgetUser 被遗忘权（P6）：删除某用户全部会话，返回被删会话 ID。
func (s *InMemoryStore) ForgetUser(user string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, sess := range s.items {
		if sess.User == user {
			delete(s.items, id)
			ids = append(ids, id)
		}
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
