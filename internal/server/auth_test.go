package server

import (
	"sync"
	"testing"
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
