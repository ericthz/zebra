package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/task"
	"github.com/ericthz/zebra/internal/tool"
)

// leakyProvider 返回内部细节报错（模拟 provider 网络/鉴权失败）。
type leakyProvider struct{}

func (p *leakyProvider) Name() string { return "leaky" }
func (p *leakyProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{}, &leakyError{}
}
func (p *leakyProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

type leakyError struct{}

func (e *leakyError) Error() string {
	return "POST https://internal-llm.example/v1/chat/completions: 401 invalid key"
}

// TestInternalErrorMasked P1-10：provider 内部错误（URL/密钥/堆栈细节）
// 不得回给客户端，客户端只看到通用文案；细节仅进日志。
func TestInternalErrorMasked(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&leakyProvider{}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
	})
	h := api.Handler()

	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(`{"message":"你好"}`))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("provider 失败应 500，实际 %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "internal-llm") || strings.Contains(body, "401 invalid key") {
		t.Fatalf("内部错误细节泄露给客户端: %s", body)
	}
	if !strings.Contains(body, "internal error") {
		t.Fatalf("应回通用文案 internal error，实际: %s", body)
	}
}

// TestModerationErrorUserVisible P1-10：内容审核拦截属用户应知信息，
// 文案需保留（用户需要知道"为什么被拒"），不套通用 internal error。
func TestModerationErrorUserVisible(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "教你赌博下注"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
		Moderator:  safety.NewKeywordModerator("赌博"),
	})
	h := api.Handler()

	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(`{"message":"你好"}`))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("输出被拦截应 500，实际 %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "未通过内容审核") {
		t.Fatalf("审核拦截原因应可见，实际: %s", rr.Body.String())
	}
}

// 确认 UserFacingError 类型断言工作。
func TestUserFacingErrorHelper(t *testing.T) {
	if got := userFacingError(&agent.UserFacingError{Msg: "输出未通过内容审核: x"}); got != "输出未通过内容审核: x" {
		t.Fatalf("UserFacingError 应原样透传，实际: %s", got)
	}
	if got := userFacingError(&leakyError{}); got != "internal error" {
		t.Fatalf("内部错误应转 internal error，实际: %s", got)
	}
}

// TestRedactErr S-2：工具失败错误（可能携带命令完整输出/机密）写入日志前
// 必须脱敏+截断，不能原样落日志。
func TestRedactErr(t *testing.T) {
	if redactErr(nil) != "" {
		t.Fatal("nil 错误应返回空串")
	}
	// 错误里内嵌 API Key 与超长命令输出
	err := fmt.Errorf("命令执行失败: exit status 1\nsk-ABCDEFGHIJKLMNOP 长输出长输出长输出长输出")
	got := redactErr(err)
	if strings.Contains(got, "ABCDEFGHIJKLMNOP") {
		t.Fatalf("API Key 应被脱敏，实际: %s", got)
	}
	if len(got) > 300 {
		t.Fatalf("错误摘要应截断，实际长度 %d", len(got))
	}
	// 正常错误（无机密）保留可读性
	if !strings.Contains(redactErr(fmt.Errorf("命令执行失败")), "命令执行失败") {
		t.Fatal("非机密错误不应被过度脱敏")
	}
}

// TestTaskRunLoadsSessionHistory F-5：任务执行必须以会话现有历史为上文
// （"继续上一条分析"类任务），无检查点时不从空历史重跑。
func TestTaskRunLoadsSessionHistory(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "ok"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
	})

	// 先建会话并写入已有历史
	sess, err := api.deps.Sessions.Create("alice", "default", "user", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	*sess.History() = append(*sess.History(),
		provider.Message{Role: "user", Content: "上一条：分析营收"},
		provider.Message{Role: "assistant", Content: "上一条结论"},
	)
	// 无检查点：应以上文为基继续执行（静默失败会丢上文）
	if _, _, err := api.taskRun(context.Background(), "alice", sess.ID, "继续", nil); err != nil {
		t.Fatalf("无检查点应沿用会话历史执行: %v", err)
	}
	// 历史必须保持已有多轮（不被清空）
	h := *sess.History()
	if len(h) < 2 || h[0].Content != "上一条：分析营收" {
		t.Fatalf("会话历史不应丢失，实际: %+v", h)
	}
}

// TestTaskRunCorruptCheckpoint P2-13：检查点损坏时任务应失败并报出解析错误，
// 而不是静默丢弃上文继续执行。
func TestTaskRunCorruptCheckpoint(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "ok"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
	})

	// 损坏的检查点：非 JSON 内容
	_, _, err := api.taskRun(context.Background(), "alice", "s1", "继续", []byte("{broken"))
	if err == nil || !strings.Contains(err.Error(), "检查点解析失败") {
		t.Fatalf("损坏检查点应报错，实际: %v", err)
	}

	// 合法检查点：正常继续
	good, _ := json.Marshal([]provider.Message{{Role: "user", Content: "上一轮"}})
	reply, _, err := api.taskRun(context.Background(), "alice", "s1", "继续", good)
	if err != nil {
		t.Fatalf("合法检查点不应报错: %v", err)
	}
	if reply == "" {
		t.Fatal("合法检查点应正常执行返回结果")
	}
}

// TestEmptyMessageRejected P2-12：空消息不得进入 Agent（会浪费一次调用且
// 污染历史），chat/stream/tasks 一律 400。
func TestEmptyMessageRejected(t *testing.T) {
	keys := NewKeyStore()
	keys.Register(Principal{Key: "admin-key", User: "admin", Role: "admin", Tenant: "default"})
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "ok"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
		TaskStore:  task.NewInMemoryStore(),
	})
	h := api.Handler()

	do := func(method, path, key, body string) int {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	for _, tc := range []struct{ name, path, body string }{
		{"chat 空消息", "/v1/chat", `{"message":""}`},
		{"chat 纯空格", "/v1/chat", `{"message":"   "}`},
		{"stream 空消息", "/v1/chat/stream", `{"message":""}`},
		{"tasks 空消息", "/v1/tasks", `{"message":""}`},
	} {
		if code := do("POST", tc.path, "user-key", tc.body); code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，实际 %d", tc.name, code)
		}
	}
}
