package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/redis"
	"github.com/ericthz/zebra/internal/redistest"
	"github.com/ericthz/zebra/internal/tool"
)

func TestRedisSessionStore(t *testing.T) {
	fs := redistest.New(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.Addr(), Timeout: 2 * time.Second}, 30*time.Minute)

	// 创建 → 读取
	sess, err := store.Create("alice", "default", "user", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.User != "alice" {
		t.Fatalf("会话元数据错误: %+v", sess)
	}
	got, ok := store.Get(sess.ID)
	if !ok || got.User != "alice" {
		t.Fatalf("应从 Redis 读到会话: %+v %v", got, ok)
	}

	// 历史写回（HistoryPersister）：修改后 Save → 再读仍保留（多轮不丢）
	*got.History() = append(*got.History(), provider.Message{Role: "user", Content: "第一轮"})
	if err := store.Save(got); err != nil {
		t.Fatal(err)
	}
	got2, ok := store.Get(sess.ID)
	if !ok || len(*got2.History()) != 1 || (*got2.History())[0].Content != "第一轮" {
		t.Fatalf("历史写回失败: %+v %v", got2, ok)
	}

	// Touch 续期：60ms 会过期，Touch 后延长到 30min
	short, _ := store.Create("bob", "default", "user", 60*time.Millisecond)
	if ok := store.Touch(short.ID); !ok {
		t.Fatal("Touch 应成功")
	}
	time.Sleep(120 * time.Millisecond)
	if _, ok := store.Get(short.ID); !ok {
		t.Fatal("Touch 续期后不应过期")
	}

	// 不存在的会话 Touch → false
	if store.Touch("ghost-session") {
		t.Fatal("不存在会话 Touch 应返回 false")
	}
}

// TestRedisSessionTouchSurvivesSave 六5：Touch 续期后 Save 不得把剩余寿命
// 覆盖回"创建时剩余"（否则活跃会话恰好 30 分钟后过期）。
func TestRedisSessionTouchSurvivesSave(t *testing.T) {
	fs := redistest.New(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.Addr(), Timeout: 2 * time.Second}, 30*time.Minute)

	// 短 TTL 会话：创建 100ms
	sess, err := store.Create("alice", "default", "user", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// 续期：Redis TTL 恢复到 30 分钟（s.ttl）
	if !store.Touch(sess.ID) {
		t.Fatal("Touch 应成功")
	}
	// 紧接着 Save（模拟一次对话写回）：不得用 time.Until(sess.Expires) 抹掉续期
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	// 等 300ms（远超创建时 100ms）：若 Save 抹掉续期，会话早已过期
	time.Sleep(300 * time.Millisecond)
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("Touch 续期后经 Save 不应过期（六5 修复）")
	}
}

func TestRedisSessionForgetUser(t *testing.T) {
	fs := redistest.New(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.Addr()}, time.Minute)

	a1, _ := store.Create("alice", "default", "user", time.Minute)
	a2, _ := store.Create("alice", "default", "user", time.Minute)
	b1, _ := store.Create("bob", "default", "user", time.Minute)

	deleted := store.ForgetUser("alice")
	if len(deleted) != 2 {
		t.Fatalf("应删除 alice 的 2 个会话，实际 %d: %v", len(deleted), deleted)
	}
	if _, ok := store.Get(a1.ID); ok {
		t.Fatal("alice 会话应被删除")
	}
	if _, ok := store.Get(a2.ID); ok {
		t.Fatal("alice 会话应被删除")
	}
	if _, ok := store.Get(b1.ID); !ok {
		t.Fatal("bob 会话不应被误删")
	}
}

// TestRedisSessionConcurrentChat Redis 会话模式下同一会话并发请求仍串行
// （六1：Get 每次重建 Session，runMu 必须按 ID 共享，否则丢轮次/数据竞争）。
func TestRedisSessionConcurrentChat(t *testing.T) {
	fs := redistest.New(t)
	redisStore := NewRedisSessionStore(&redis.Client{Addr: fs.Addr(), Timeout: 2 * time.Second}, 30*time.Minute)

	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "25 度，晴朗。"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   redisStore,
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
	})
	h := api.Handler()

	// 建会话
	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(`{"message":"你好"}`))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("建会话应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	var first ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"session_id":%q,"message":"并发第%d轮"}`, first.SessionID, i)
			r := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(body))
			r.Header.Set("Authorization", "Bearer user-key")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("并发第%d轮应 200，实际 %d body=%s", i, w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()

	// 串行化后 Redis 中历史条数应精确：(n+1) 轮 × 2 条
	sess, ok := redisStore.Get(first.SessionID)
	if !ok {
		t.Fatal("会话应存在于 Redis")
	}
	if want := (n + 1) * 2; len(*sess.History()) != want {
		t.Fatalf("Redis 会话历史应为 %d 条（无丢失/重叠），实际 %d: %+v", want, len(*sess.History()), *sess.History())
	}
}

// TestRedisForgetNoResurrection P0-2：ForgetUser 删除会话后，迟到的对话
// 请求必须报 401（lockSession 锁内重取失败），绝不能基于陈旧快照继续执行
// 并 persistHistory 把已删会话写回复活。
func TestRedisForgetNoResurrection(t *testing.T) {
	fs := redistest.New(t)
	store := NewRedisSessionStore(&redis.Client{Addr: fs.Addr(), Timeout: 2 * time.Second}, 30*time.Minute)

	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})
	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "25 度。"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   store,
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
	})
	h := api.Handler()

	// 建会话
	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(`{"message":"你好"}`))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("建会话应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	var first ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}

	// 在飞对话持锁（等价 chat 正在执行）；forget 必须等它结束
	sess, ok := store.Get(first.SessionID)
	if !ok {
		t.Fatal("会话应存在")
	}
	sess.runMu.Lock()
	forgetDone := make(chan struct{})
	go func() {
		defer close(forgetDone)
		store.ForgetUser("alice")
	}()
	select {
	case <-forgetDone:
		t.Fatal("ForgetUser 不应在会话锁未释放时完成")
	case <-time.After(30 * time.Millisecond):
	}
	sess.runMu.Unlock()
	<-forgetDone

	// 迟到的同会话请求：不得复活会话，必须 401
	r2 := httptest.NewRequest("POST", "/v1/chat",
		bytes.NewBufferString(fmt.Sprintf(`{"session_id":%q,"message":"迟到"}`, first.SessionID)))
	r2.Header.Set("Authorization", "Bearer user-key")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("已删会话请求应 401，实际 %d body=%s（会话被复活）", rr2.Code, rr2.Body.String())
	}
	if _, ok := store.Get(first.SessionID); ok {
		t.Fatal("被遗忘会话不应复活")
	}
}
