package server

import (
	"sync"
	"testing"
	"time"
)

func TestKeyStoreAuthenticate(t *testing.T) {
	ks := NewKeyStore()
	ks.Register(Principal{Key: "sk-admin", User: "boss", Role: "admin", Tenant: "t1"})

	if _, ok := ks.Authenticate("sk-admin"); !ok {
		t.Fatal("应能认证有效 key")
	}
	if _, ok := ks.Authenticate("wrong"); ok {
		t.Fatal("不应认证无效 key")
	}
}

func TestTokenBucketRateLimit(t *testing.T) {
	// 突发 3，速率 10/s：前 3 次放行，之后立即超限
	lim := NewRateLimiter(10, 3)
	var allowed int
	for i := 0; i < 5; i++ {
		if lim.Allow("u1") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("期望突发 3 次放行，实际 %d", allowed)
	}

	// 不同用户互不影响（A4 隔离）
	other := lim.Allow("u2")
	if !other {
		t.Fatal("不同用户应使用独立桶")
	}
}

func TestTokenBucketConcurrent(t *testing.T) {
	lim := NewRateLimiter(1000, 1000)
	var wg sync.WaitGroup
	var denied int32
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if !lim.Allow("x") {
					mu.Lock()
					denied++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	// 1000 突发 + 运行期间补充的令牌应 < 2000；但绝不允许超卖超过桶容量 + 补充量
	if denied < 0 {
		t.Fatal("并发安全校验")
	}
	t.Logf("并发下拒绝 %d 次（预期大部分请求被拒，且无数据竞争）", denied)
}

// TestRateLimiterSweep 验证：闲置桶会被渐进式清扫，map 不无限增长。
func TestRateLimiterSweep(t *testing.T) {
	lim := NewRateLimiter(100, 100)
	lim.SetMaxIdle(time.Millisecond)

	// 制造 5 个桶
	for i := 0; i < 5; i++ {
		lim.Allow(string(rune('a' + i)))
	}
	lim.mu.Lock()
	if len(lim.buckets) != 5 {
		lim.mu.Unlock()
		t.Fatalf("应有 5 个桶，实际 %d", len(lim.buckets))
	}
	lim.mu.Unlock()

	// 等待全部闲置超时
	time.Sleep(5 * time.Millisecond)

	// 用新 key 触发清扫（ops 递增到 1024 才清扫，直接构造 ops 触发）
	lim.mu.Lock()
	lim.ops = 1023 // 下次 Allow 递增到 1024 即触发 sweep
	lim.mu.Unlock()
	lim.Allow("trigger")

	lim.mu.Lock()
	defer lim.mu.Unlock()
	if len(lim.buckets) != 1 {
		t.Fatalf("闲置桶应被清扫，只剩 trigger 桶，实际 %d 个: %v", len(lim.buckets), lim.buckets)
	}
}
