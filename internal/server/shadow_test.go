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
	"sync"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/eval"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// staticProvider 固定回答的模型（Agent 主模型 / 候选模型共用；name 可区分）。
type staticProvider struct {
	reply string
	name  string
}

func (p *staticProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "static"
}
func (p *staticProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: p.reply}, nil
}
func (p *staticProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// judgeProvider 按"回答是否含候选标记"返回不同分数（模拟评审模型的双评差异）。
type judgeProvider struct{}

func (judgeProvider) Name() string { return "judge" }
func (judgeProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	last := msgs[len(msgs)-1].Content
	if strings.Contains(last, "候选回答") {
		return provider.Message{Content: `{"faithfulness":0.9,"relevance":0.9,"safety":0.9}`}, nil
	}
	return provider.Message{Content: `{"faithfulness":0.4,"relevance":0.4,"safety":0.4}`}, nil
}
func (judgeProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// newShadowServer 构造带影子评测的服务（主模型固定回答，评审模型脚本化）。
func newShadowServer(t *testing.T) (http.Handler, *eval.ShadowStore, *APIServer) {
	t.Helper()
	keys := NewKeyStore()
	keys.Register(Principal{Key: "admin-key", User: "admin", Role: "admin", Tenant: "default"})
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})

	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。角色：{role}。"})

	store := eval.NewShadowStore(10)
	shadow := eval.NewShadowEvaluator(
		&staticProvider{reply: "候选回答：北京多云转晴。", name: "candidate-model"},
		eval.NewJudge(provider.NewRouter(judgeProvider{})),
		store, 1.0, // 采样率 100%：方便端到端断言
	)

	api := NewAPIServer(Deps{
		Router: provider.NewRouter(
			&staticProvider{reply: "主回答：北京 25 度，晴朗。", name: "primary-model"},
			&staticProvider{reply: "候选回答：北京多云转晴。", name: "candidate-model"},
		),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
		Model:      "primary-model",
		Shadow:     shadow,
	})
	return api.Handler(), store, api
}

func TestShadowAPI(t *testing.T) {
	h, store, api := newShadowServer(t)

	do := func(method, path, key string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 1. user 角色触发影子 → 403（评测属运维动作，RBAC 收敛）
	rr := do("POST", "/v1/eval/shadow", "user-key", `{"message":"北京天气怎么样？"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("user 应 403，实际 %d", rr.Code)
	}

	// 2. admin 触发影子 → 200，主回答正常返回 + 影子结论 candidate_better
	rr = do("POST", "/v1/eval/shadow", "admin-key", `{"message":"北京天气怎么样？"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin 应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Reply  string            `json:"reply"`
		Shadow eval.ShadowResult `json:"shadow"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Reply, "主回答") {
		t.Fatalf("主回答异常: %q", resp.Reply)
	}
	if resp.Shadow.Verdict != eval.VerdictCandidateBetter {
		t.Fatalf("候选得分更高，verdict 应为 candidate_better，实际 %s", resp.Shadow.Verdict)
	}

	// 3. 记录已落库，运维可查
	recent := store.Recent("", 1)
	if len(recent) != 1 || recent[0].Verdict != eval.VerdictCandidateBetter {
		t.Fatalf("影子记录未落库: %+v", recent)
	}
	rr = do("GET", "/v1/eval/shadow", "admin-key", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "candidate_better") {
		t.Fatalf("影子记录列表异常: %d %s", rr.Code, rr.Body.String())
	}

	// 4. 影子评测同属一次正常对话，历史应写回（/v1/chat 对齐）
	sessID := resp.Shadow.Session
	if sessID != "" {
		sess, ok := api.deps.Sessions.Get(sessID)
		if !ok {
			t.Fatal("影子评测应落到已存在会话")
		}
		if len(*sess.History()) < 2 {
			t.Fatalf("影子评测后历史应写回，实际 %d 条", len(*sess.History()))
		}
	}
}

// TestChatConsistentMode 自一致性：/v1/chat 的 mode=consistent 走独立采样择优。
func TestChatConsistentMode(t *testing.T) {
	h, _, _ := newShadowServer(t)
	do := func(key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	rr := do("user-key", `{"message":"北京天气怎么样？","mode":"consistent"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("mode=consistent 应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "主回答") {
		t.Fatalf("自一致性应返回主模型回答（采样后择优），实际: %s", rr.Body.String())
	}
}

// TestSessionConcurrentChat 同一会话并发请求：历史不撕裂（Session.runMu 串行化）。
func TestSessionConcurrentChat(t *testing.T) {
	h, _, api := newShadowServer(t)
	// 先建会话，再并发打同一会话
	req := httptest.NewRequest("POST", "/v1/chat", bytes.NewBufferString(`{"message":"你好"}`))
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("建会话请求应 200，实际 %d body=%s", rr.Code, rr.Body.String())
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

	// 串行化后历史条数应精确：1 建会话轮 + n 并发轮 = (n+1) 轮 × 2 条
	sess, ok := api.deps.Sessions.Get(first.SessionID)
	if !ok {
		t.Fatal("会话应存在")
	}
	if want := (n + 1) * 2; len(*sess.History()) != want {
		t.Fatalf("并发后历史应为 %d 条（无丢失/重叠），实际 %d: %+v", want, len(*sess.History()), *sess.History())
	}
}

// TestShadowDashboardAndPromote 看板统计 + 灰度切换。
func TestShadowDashboardAndPromote(t *testing.T) {
	h, _, _ := newShadowServer(t)
	do := func(method, path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(""))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 跑一次影子（产生 candidate_better 记录）
	req := httptest.NewRequest("POST", "/v1/eval/shadow", bytes.NewBufferString(`{"message":"北京天气"}`))
	req.Header.Set("Authorization", "Bearer admin-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("影子评测失败: %d %s", rr.Code, rr.Body.String())
	}

	// 看板：统计含 1 条 candidate_better
	rr = do("GET", "/v1/eval/shadow/stats", "admin-key")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"candidate_better":1`) {
		t.Fatalf("看板统计异常: %d %s", rr.Code, rr.Body.String())
	}
	// user 无权限
	if rr := do("GET", "/v1/eval/shadow/stats", "user-key"); rr.Code != http.StatusForbidden {
		t.Fatalf("user 看板应 403，实际 %d", rr.Code)
	}

	// 灰度切换：候选提升为主模型
	rr = do("POST", "/v1/eval/shadow/promote", "admin-key")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "candidate-model") {
		t.Fatalf("promote 异常: %d %s", rr.Code, rr.Body.String())
	}
	// 切换后看板 primary 应为候选模型
	rr = do("GET", "/v1/eval/shadow/stats", "admin-key")
	if !strings.Contains(rr.Body.String(), `"primary":"candidate-model"`) {
		t.Fatalf("切换后主模型应为候选: %s", rr.Body.String())
	}
}

// TestShadowAutoRollback 金丝雀自动回滚：promote 后胜率不达标自动切回原主。
func TestShadowAutoRollback(t *testing.T) {
	h, store, api := newShadowServer(t)
	do := func(method, path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(""))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// promote：主模型切为候选（candidate-model），记录原主 primary-model；
	// 影子候选同步重指向原主（primary-model），对比"新主 vs 原主"。
	if rr := do("POST", "/v1/eval/shadow/promote", "admin-key"); rr.Code != http.StatusOK {
		t.Fatalf("promote 失败: %d %s", rr.Code, rr.Body.String())
	}
	if got := api.deps.Router.Primary().Name(); got != "candidate-model" {
		t.Fatalf("promote 后主模型应为 candidate-model，实际 %s", got)
	}
	if got := api.deps.Shadow.CandidateName(); got != "primary-model" {
		t.Fatalf("promote 后影子候选应重指向原主 primary-model，实际 %s（避免自我对比）", got)
	}

	// 造出"原主反超"样本：10 条有效记录中候选（原主 primary-model）赢 8 条
	// → 说明新主 candidate-model 质量回退，应触发自动回滚。
	now := time.Now()
	for i := 0; i < 8; i++ {
		store.Add(&eval.ShadowResult{Time: now, Verdict: eval.VerdictCandidateBetter})
	}
	for i := 0; i < 2; i++ {
		store.Add(&eval.ShadowResult{Time: now, Verdict: eval.VerdictPrimaryBetter})
	}

	// 触发回滚检查 → 自动切回原主
	api.maybeShadowRollback()
	if got := api.deps.Router.Primary().Name(); got != "primary-model" {
		t.Fatalf("原主反超应自动回滚到 primary-model，实际 %s", got)
	}
	// 回滚后候选重指向回滚的模型（candidate-model），恢复"候选 vs 主"对比
	if got := api.deps.Shadow.CandidateName(); got != "candidate-model" {
		t.Fatalf("回滚后影子候选应重指向 candidate-model，实际 %s", got)
	}
	// 回滚后观察期清空，再次检查不动作
	api.maybeShadowRollback()
	if got := api.deps.Router.Primary().Name(); got != "primary-model" {
		t.Fatalf("回滚后不应再次切换: %s", got)
	}
}
