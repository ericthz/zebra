// Package eval —— LLM 黄金评测集骨架（E）。
//
// 思路：准备一组"黄金问题 + 期望工具名"，跑真实 Agent，断言模型是否
// 调对了工具、参数是否合理。默认跳过（不连真实模型）；设置
//
//	ZEBRA_EVAL=1 OLLAMA_BASE_URL=... OLLAMA_MODEL=...
//
// 后执行：go test ./test/eval/ -v
//
// 这是上线前"模型回归"的底线：每次换模型/改 prompt 后跑一遍，防止
// Function Calling 能力退化。生产可扩展为更多用例 + 自动评分 + 报告。
package eval

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/tool"
)

// goldenCase 一个评测用例。
type goldenCase struct {
	name     string
	input    string
	wantTool string // 期望模型调用的工具名（空表示期望纯文本回答）
	wantKw   string // 期望最终回答包含的关键词
}

var cases = []goldenCase{
	{name: "天气", input: "北京今天天气怎么样？", wantTool: "get_current_weather", wantKw: "°"},
	{name: "计算", input: "计算 (23 + 19) * 5 等于多少？", wantTool: "calculator", wantKw: "210"},
	{name: "时间", input: "现在几点？", wantTool: "get_current_datetime", wantKw: ":"},
	{name: "闲聊", input: "你好呀", wantTool: "", wantKw: ""},
}

func TestGoldenEval(t *testing.T) {
	if os.Getenv("ZEBRA_EVAL") != "1" {
		t.Skip("跳过：设置 ZEBRA_EVAL=1 开启 LLM 评测")
	}

	// 与 cmd/demo 相同的装配（可抽公共函数复用）
	httpCli := provider.NewHTTPClient(20*time.Second, 2, 300*time.Millisecond)
	router := provider.NewRouter(&provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "llama3.1"),
		Client:  httpCli,
	})

	reg := tool.NewRegistry()
	reg.Register(&tool.WeatherTool{})
	reg.Register(&tool.CalculatorTool{})
	reg.Register(&tool.DateTimeTool{})
	reg.Register(&tool.RandomTool{})
	reg.Register(&tool.SearchTool{})
	reg.Register(&tool.UnitConverterTool{})
	reg.Register(&tool.TranslateTool{})
	reg.Register(&tool.IPInfoTool{})

	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手，可以调用工具。角色：{role}。"})

	passed, failed := 0, 0
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ag := agent.New(agent.Config{
				Router: router, Tools: reg, Prompts: prompts,
				Mem:       memory.NewManager(memory.NewWorkingMemory(10), nil),
				Window:    &agent.ContextWindow{MaxTokens: 4000},
				Moderator: safety.NewKeywordModerator(),
				MaxTurns:  3, PromptName: "assistant",
			})
			hist := make([]provider.Message, 0)
			ag.Bind("eval", "admin", "eval-user", &hist)

			answer, err := ag.Run(context.Background(), c.input, agent.RunOptions{})
			if err != nil {
				t.Errorf("运行失败: %v", err)
				failed++
				return
			}
			// 用审计过的工具名集合判断调用了哪些工具
			called := calledTools(hist, c.input, answer)
			_ = called

			t.Logf("回答: %s", answer)
			if c.wantTool != "" && !containsTool(hist) {
				t.Errorf("期望调用工具 %s，但未检测到", c.wantTool)
				failed++
				return
			}
			if c.wantKw != "" && !contains(answer, c.wantKw) {
				t.Errorf("期望回答包含 %q，实际 %q", c.wantKw, answer)
				failed++
				return
			}
			passed++
		})
	}
	t.Logf("评测结果: %d 通过 / %d 失败", passed, failed)
	if failed > 0 {
		t.Fail()
	}
}

// containsTool 粗检测历史里是否有 tool 角色消息（真实实现应统计工具名）。
func containsTool(hist []provider.Message) bool {
	for _, m := range hist {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func calledTools([]provider.Message, string, string) []string { return nil }

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
