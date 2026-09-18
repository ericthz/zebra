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
	"sort"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/eval"
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
	// 时间：模型可能把时间格式化成中文（"晚上11点"），不带 ":"；工具调用
	// 才是可靠信号，故 wantKw 留空。
	{name: "时间", input: "现在几点？", wantTool: "get_current_datetime", wantKw: ""},
	{name: "闲聊", input: "你好呀", wantTool: "", wantKw: ""},
}

func TestGoldenEval(t *testing.T) {
	if os.Getenv("ZEBRA_EVAL") != "1" {
		t.Skip("跳过：设置 ZEBRA_EVAL=1 开启 LLM 评测")
	}

	// 与 cmd/zebra 相同的装配（可抽公共函数复用）
	httpCli := provider.NewHTTPClient(20*time.Second, 2, 300*time.Millisecond)
	router := provider.NewRouter(&provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
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
			called := make(map[string]bool)
			ag := agent.New(agent.Config{
				Router: router, Tools: reg, Prompts: prompts,
				Mem:       memory.NewManager(memory.NewWorkingMemory(10), nil),
				Window:    &agent.ContextWindow{MaxTokens: 4000},
				Moderator: safety.NewKeywordModerator(),
				MaxTurns:  3, PromptName: "assistant",
				// 工具调用检测：用 OnTool 回调收集（历史只写 user/assistant，
				// 工具消息不进 hist，故不能靠扫描历史判断调用了哪些工具）。
				OnTool: func(name string, _ map[string]interface{}, _ bool, _ error) {
					called[name] = true
				},
			})
			hist := make([]provider.Message, 0)
			ag.Bind("eval", "admin", "eval-user", &hist)

			answer, err := ag.Run(context.Background(), c.input, agent.RunOptions{})
			if err != nil {
				t.Errorf("运行失败: %v", err)
				failed++
				return
			}
			t.Logf("回答: %s", answer)
			if c.wantTool != "" && !called[c.wantTool] {
				t.Errorf("期望调用工具 %s，实际调用: %v", c.wantTool, keys(called))
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

// keys 提取 map 键集合（已排序，供诊断输出）。
func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// TestJudgeClosedLoop —— 质量闭环演示：
// 跑真实 Agent → 用 Judge 模型给每个回答自动打分 → 汇总通过率。
// 开启方式：ZEBRA_EVAL=1 go test ./test/eval/ -v
func TestJudgeClosedLoop(t *testing.T) {
	if os.Getenv("ZEBRA_EVAL") != "1" {
		t.Skip("跳过：设置 ZEBRA_EVAL=1 开启 LLM 评测")
	}
	// 本地小模型做 Judge 很慢（实测 4b 长回答约 60s+），默认 20s HTTP /
	// 30s Judge 会误判为超时：Judge 慢是算力问题，不是能力退化，不应因此误报。
	httpCli := provider.NewHTTPClient(150*time.Second, 2, 300*time.Millisecond)
	router := provider.NewRouter(&provider.OllamaProvider{
		BaseURL: envOr("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:   envOr("OLLAMA_MODEL", "qwen3.5:0.8b-mlx"),
		Client:  httpCli,
	})
	judge := eval.NewJudge(router)
	judge.Timeout = 150 * time.Second

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

	var passed, total int
	for _, c := range cases {
		total++
		// 与 TestGoldenEval 相同装配跑真实 Agent，把真实回答交给 Judge 打分
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
			t.Logf("agent 运行失败 %s: %v", c.name, err)
			continue
		}
		s, err := judge.Score(context.Background(), c.input, answer)
		if err != nil {
			t.Logf("judge 失败 %s: %v", c.name, err)
			continue
		}
		if s.Pass(0.7) {
			passed++
			t.Logf("✓ %s: 忠实=%.2f 相关=%.2f 安全=%.2f — %s", c.name, s.Faithfulness, s.Relevance, s.Safety, s.Comment)
		} else {
			t.Logf("⚠ %s: 忠实=%.2f 相关=%.2f 安全=%.2f — %s", c.name, s.Faithfulness, s.Relevance, s.Safety, s.Comment)
		}
	}
	t.Logf("Judge 自动评分: %d/%d 通过", passed, total)
}
