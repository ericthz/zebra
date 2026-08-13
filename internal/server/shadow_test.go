package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	h, store, _ := newShadowServer(t)

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
}

// TestShadowDashboardAndPromote 看板统计 + 灰度切换（P26）。
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

// TestShadowAutoRollback P42 金丝雀自动回滚：promote 后胜率不达标自动切回原主。
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

	// promote：主模型切为候选（candidate-model），记录原主 primary-model
	if rr := do("POST", "/v1/eval/shadow/promote", "admin-key"); rr.Code != http.StatusOK {
		t.Fatalf("promote 失败: %d %s", rr.Code, rr.Body.String())
	}
	if got := api.deps.Router.Primary().Name(); got != "candidate-model" {
		t.Fatalf("promote 后主模型应为 candidate-model，实际 %s", got)
	}

	// 造出低胜率样本：10 条有效记录中 candidate 只赢 2 条
	now := time.Now()
	for i := 0; i < 8; i++ {
		store.Add(&eval.ShadowResult{Time: now, Verdict: eval.VerdictPrimaryBetter})
	}
	for i := 0; i < 2; i++ {
		store.Add(&eval.ShadowResult{Time: now, Verdict: eval.VerdictCandidateBetter})
	}

	// 触发回滚检查 → 自动切回原主
	api.maybeShadowRollback()
	if got := api.deps.Router.Primary().Name(); got != "primary-model" {
		t.Fatalf("胜率不达标应自动回滚到 primary-model，实际 %s", got)
	}
	// 回滚后观察期清空，再次检查不动作
	api.maybeShadowRollback()
	if got := api.deps.Router.Primary().Name(); got != "primary-model" {
		t.Fatalf("回滚后不应再次切换: %s", got)
	}
}
