// A3 认证鉴权 + B6 限流配额。
//
//	A3 API Key + RBAC：Bearer Token 认证，admin/user 两级角色；
//	   角色→工具权限的具体收敛在 tool.Registry 白名单（D20）。
//	B6 令牌桶限流：按身份维度（user）限流，防滥用；配额计量由 Metrics 完成。
package server

import (
	"sync"
	"time"
)

// Principal 认证主体（每个 API Key 绑定身份）。
type Principal struct {
	Key    string `json:"key"`
	User   string `json:"user"`
	Role   string `json:"role"`   // "admin" | "user"
	Tenant string `json:"tenant"` // A4 租户隔离标识
}

// KeyStore 内存 API Key 存储（生产替换为数据库/密钥管理系统）。
type KeyStore struct {
	mu   sync.RWMutex
	keys map[string]Principal
}

// NewKeyStore 构造。
func NewKeyStore() *KeyStore {
	return &KeyStore{keys: make(map[string]Principal)}
}

// Register 注册一个 API Key。
func (k *KeyStore) Register(p Principal) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys[p.Key] = p
}

// Authenticate 校验 Bearer 凭据，返回主体。
func (k *KeyStore) Authenticate(bearer string) (Principal, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	p, ok := k.keys[bearer]
	return p, ok
}

// ---------------- B6 令牌桶限流 ----------------

// TokenBucket 单桶令牌桶（均匀速率 + 突发容量）。
type TokenBucket struct {
	mu     sync.Mutex
	rate   float64 // tokens/秒
	burst  float64 // 桶容量
	tokens float64
	last   time.Time
}

func newTokenBucket(rate, burst float64) *TokenBucket {
	return &TokenBucket{rate: rate, burst: burst, tokens: burst, last: time.Now()}
}

// Allow 取一个令牌。
func (b *TokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens = minFloat(b.burst, b.tokens+elapsed*b.rate)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimiter 按 key（如 user/tenant）分桶限流。
type RateLimiter struct {
	mu       sync.Mutex
	rate     float64
	burst    float64
	maxIdle  time.Duration // 桶闲置超时，超时后清扫（防 map 无限增长）
	buckets  map[string]*TokenBucket
	lastUsed map[string]time.Time // 每桶最近访问时间
	ops      int64                // 触发清扫的计数器
}

// NewRateLimiter 构造。rate 每秒放行数，burst 突发容量。
// maxIdle 为桶闲置清理阈值（0 = 默认 10 分钟）。
func NewRateLimiter(rate, burst float64) *RateLimiter {
	return &RateLimiter{
		rate: rate, burst: burst, maxIdle: 10 * time.Minute,
		buckets: make(map[string]*TokenBucket), lastUsed: make(map[string]time.Time),
	}
}

// SetMaxIdle 设置桶闲置清理阈值。
func (l *RateLimiter) SetMaxIdle(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxIdle = d
}

// Allow 对指定 key 限流判断。
func (l *RateLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = newTokenBucket(l.rate, l.burst)
		l.buckets[key] = b
	}
	l.lastUsed[key] = now
	// 渐进式清扫：每 1024 次访问顺带清理一次闲置桶，
	// 防恶意随机 key 撑爆 map（无固定后台 goroutine，保持零依赖简单性）。
	l.ops++
	if l.ops%1024 == 0 {
		l.sweepLocked(now)
	}
	l.mu.Unlock()
	return b.Allow()
}

// sweepLocked 清理闲置超时的桶（调用方须持锁）。
func (l *RateLimiter) sweepLocked(now time.Time) {
	idle := l.maxIdle
	if idle <= 0 {
		return
	}
	for k, last := range l.lastUsed {
		if now.Sub(last) > idle {
			delete(l.buckets, k)
			delete(l.lastUsed, k)
		}
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
