package supervisor

import (
	"context"
	"testing"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// fakeAgentProvider 直接返回固定文本（worker 的 agent 会走 Run）。
type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: "worker-reply"}, nil
}
func (fakeProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// 构造一个 worker 工厂（返回绑定可用配置的最小 agent）。
func makeWorker(name, desc string) *Worker {
	return &Worker{
		Name: name, Description: desc,
		Build: func() *agent.Agent {
			prompts := prompt.NewRegistry("z")
			prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "系统提示"})
			return agent.New(agent.Config{
				Router: provider.NewRouter(fakeProvider{}), Prompts: prompts,
				Tools: tool.NewRegistry(), MaxTurns: 1, PromptName: "assistant",
			})
		},
	}
}

func TestRouteLLM(t *testing.T) {
	// 用一个固定返回 worker 名的 provider 模拟 LLM 路由
	router := provider.NewRouter(&namedProvider{name: "data"})
	sup := NewSupervisor(router,
		makeWorker("data", "擅长计算、换算、日期时间"),
		makeWorker("knowledge", "擅长搜索、查资料"),
	)
	w, err := sup.Route(context.Background(), "算一下 12*8")
	if err != nil || w == nil || w.Name != "data" {
		t.Fatalf("LLM 路由应命中 data，got %v err=%v", w, err)
	}
}

func TestRouteFallback(t *testing.T) {
	// 无 router → 关键词兜底
	sup := NewSupervisor(nil,
		makeWorker("data", "擅长计算、换算、日期时间"),
		makeWorker("knowledge", "擅长搜索、查资料、抓网页"),
	)
	w, _ := sup.Route(context.Background(), "帮我搜索一下新闻")
	if w.Name != "knowledge" {
		t.Fatalf("关键词兜底应命中 knowledge，got %s", w.Name)
	}
}

func TestRunBindsSession(t *testing.T) {
	sup := NewSupervisor(provider.NewRouter(&namedProvider{name: "data"}),
		makeWorker("data", "擅长计算"),
	)
	hist := make([]provider.Message, 0)
	reply, w, err := sup.Run(context.Background(), "算一下", "s1", "admin", "u", &hist, agent.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "data" || reply != "worker-reply" {
		t.Fatalf("Run 异常: worker=%s reply=%s", w.Name, reply)
	}
}

// namedProvider 固定返回某 worker 名的路由 provider。
type namedProvider struct{ name string }

func (n *namedProvider) Name() string { return "router" }
func (n *namedProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Role: "assistant", Content: `{"worker":"` + n.name + `"}`}, nil
}
func (n *namedProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}
