package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// newProfileServer 构造带画像的服务（Agent 用固定回答的静态模型）。
func newProfileServer(t *testing.T) (http.Handler, *memory.ProfileStore) {
	t.Helper()
	keys := NewKeyStore()
	keys.Register(Principal{Key: "alice-key", User: "alice", Role: "user", Tenant: "default"})
	keys.Register(Principal{Key: "bob-key", User: "bob", Role: "user", Tenant: "default"})

	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。角色：{role}。"})

	profile := memory.NewProfileStore()
	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "好的，已记住。"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
		Profile:    profile,
		ProfileTTL: time.Hour,
	})
	return api.Handler(), profile
}

func TestProfileLearnViewForget(t *testing.T) {
	h, profile := newProfileServer(t)

	do := func(method, path, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 1. 对话中说出事实 → Agent 自动学习（P22 画像学习）
	rr := do("POST", "/v1/chat", "alice-key", `{"message":"我叫小明，我喜欢吃火锅。"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("chat 应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	if profile.Count("alice") != 2 {
		t.Fatalf("应学到 2 条画像事实，实际 %d", profile.Count("alice"))
	}

	// 2. 查看自己的画像：可见 name/preference
	rr = do("GET", "/v1/user/profile", "alice-key", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET profile 应 200，实际 %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "小明") || !strings.Contains(rr.Body.String(), "火锅") {
		t.Fatalf("画像应含 name/preference: %s", rr.Body.String())
	}

	// 3. 精细遗忘：删除 name，保留 preference
	rr = do("POST", "/v1/user/profile/forget", "alice-key", `{"key":"name"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("forget 应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	rr = do("GET", "/v1/user/profile", "alice-key", "")
	if strings.Contains(rr.Body.String(), "小明") || !strings.Contains(rr.Body.String(), "火锅") {
		t.Fatalf("应只遗忘 name: %s", rr.Body.String())
	}

	// 4. 租户/用户隔离：bob 看不到 alice 的画像（A4）
	rr = do("GET", "/v1/user/profile", "bob-key", "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "火锅") {
		t.Fatalf("bob 不应看到 alice 画像: %s", rr.Body.String())
	}
}

// TestProfileConflictResolveAPI P57：同名不同值记录冲突，可裁决回退旧值。
func TestProfileConflictResolveAPI(t *testing.T) {
	h, profile := newProfileServer(t)
	do := func(method, path, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	do("POST", "/v1/chat", "alice-key", `{"message":"我叫小明"}`)
	do("POST", "/v1/chat", "alice-key", `{"message":"我叫大明"}`)
	if cs := profile.ConflictsFor("alice"); len(cs) != 1 || cs[0].Key != "name" {
		t.Fatalf("应记录 name 冲突: %+v", cs)
	}

	// GET 画像应带 conflicts 字段
	rr := do("GET", "/v1/user/profile", "alice-key", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "conflicts") {
		t.Fatalf("GET profile 应含 conflicts: %d %s", rr.Code, rr.Body.String())
	}
	// 裁决回退旧值 → 画像回到"小明"
	rr = do("POST", "/v1/user/profile/resolve", "alice-key", `{"key":"name","keep":"old"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve 应 200，实际 %d %s", rr.Code, rr.Body.String())
	}
	rr = do("GET", "/v1/user/profile", "alice-key", "")
	if !strings.Contains(rr.Body.String(), "小明") {
		t.Fatalf("回退后应为小明: %s", rr.Body.String())
	}
	if len(profile.ConflictsFor("alice")) != 0 {
		t.Fatal("裁决后冲突应清空")
	}
}
