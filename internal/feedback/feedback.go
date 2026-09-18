// Package feedback 用户反馈闭环。
//
// 价值：Agent 输出质量好不好，模型自评（Judge）是"估计"，用户点赞/踩是
// "真值"。收集反馈 → 回流到评测集/优化 prompt，构成完整质量闭环：
//
//	用户点赞/踩 → 落库 + 指标 + 审计 → 用作评测真值（golden）→ 回归测试
//
// 本包实现：
//   - Feedback：一条反馈（点赞/踩 + 会话 + 可选评论）
//   - Store 接口 + InMemoryStore（生产换数据库）
//   - Counts 统计（正/负反馈），供指标与看板
//
// 生产演化方向：反馈与响应快照绑定（存回答原文供复盘）；低分自动触发
// 告警/回流评测；A/B 实验的胜负判定。
package feedback

import (
	"fmt"
	"sync"
	"time"
)

// 评分常量。
const (
	RatingUp   = 1  // 点赞
	RatingDown = -1 // 踩
)

// Feedback 一条反馈。
type Feedback struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	Session string    `json:"session"`
	Rating  int       `json:"rating"` // 1 或 -1
	Comment string    `json:"comment,omitempty"`
	Created time.Time `json:"created"`
}

// Store 反馈存储接口（可替换数据库）。
type Store interface {
	Add(f *Feedback) error
	List(user string) []*Feedback
	Counts() (positive, negative int64)
}

// InMemoryStore 内存反馈存储（并发安全）。
type InMemoryStore struct {
	mu     sync.RWMutex
	items  []*Feedback
	pos    int64
	neg    int64
	nextID int64
}

// NewInMemoryStore 构造。
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{items: make([]*Feedback, 0)}
}

// Add 追加一条反馈并更新计数。
func (s *InMemoryStore) Add(f *Feedback) error {
	if f.Rating != RatingUp && f.Rating != RatingDown {
		return fmt.Errorf("无效评分: %d", f.Rating)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	f.ID = fmt.Sprintf("fb-%d-%d", time.Now().Unix(), s.nextID)
	f.Created = time.Now()
	s.items = append(s.items, f)
	if f.Rating == RatingUp {
		s.pos++
	} else {
		s.neg++
	}
	return nil
}

// List 按用户列出反馈。
func (s *InMemoryStore) List(user string) []*Feedback {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Feedback
	for _, f := range s.items {
		if f.User == user {
			out = append(out, f)
		}
	}
	return out
}

// Counts 返回正/负反馈计数。
func (s *InMemoryStore) Counts() (int64, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pos, s.neg
}
