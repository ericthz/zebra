package observe

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/console"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// testTool 启动清单测试用最小工具。
type testTool struct{ name string }

func (t testTool) Name() string        { return t.name }
func (t testTool) Description() string { return "test" }
func (t testTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t testTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "ok", nil
}

func TestPrintInventory(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(testTool{name: "calculator"})
	reg.Register(testTool{name: "fetch_url"})

	info := Info{
		Title:           "Zebra 启动清单",
		Models:          []string{"qwen3.5:0.8b-mlx", "gpt-4o-mini"},
		Tools:           reg,
		Skills:          []*skill.Skill{{Name: "report-sop", Description: "写研究报告"}, {Name: "data-check", Description: "数据核对"}},
		MCPMode:         "http",
		MCPCount:        1,
		MCPTools:        []Item{{Name: "mcp_echo", Description: "echo tool"}},
		MemMode:         "工作记忆 + Qdrant",
		RAGDocs:         2,
		RAGChunks:       12,
		VoiceEnabled:    true,
		ShadowCandidate: "qwen2.5:7b",
		ShadowSample:    0.1,
		RedisURL:        "127.0.0.1:6379",
	}

	var buf bytes.Buffer
	PrintInventory(&buf, info)
	out := buf.String()
	for _, want := range []string{
		"Zebra 启动清单",
		": 2 个",
		"calculator", // 工具子项逐行
		"fetch_url",
		"模式=http · 已连接 1 个工具",
		"mcp_echo",
		"echo tool",
		"技能",
		"report-sop",
		"写研究报告",
		"数据核对",
		": 2 篇文档 / 12 块",
		": 已启用（ASR/TTS）",
		"candidate=qwen2.5:7b",
		": 127.0.0.1:6379（会话共享）",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("启动清单缺少 %q\n%s", want, out)
		}
	}

	// 对齐：子项树形分支（├/└）起点显示列 == 父行行首图标（▲/■）正下方
	// （用 console.Width 按显示宽度换算，避免多字节 UTF-8 的字节列干扰）
	iconCol := -1
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "▲ 工具") {
			i := strings.Index(l, "▲")
			iconCol = console.Width(l[:i])
			break
		}
	}
	if iconCol < 0 {
		t.Fatal("未找到工具父行")
	}
	for _, l := range strings.Split(out, "\n") {
		for _, item := range []string{"calculator", "report-sop"} {
			if i := strings.Index(l, item); i >= 0 {
				bi := strings.IndexAny(l, "├└")
				if bi < 0 {
					t.Fatalf("子项行缺少分支符: %q", l)
				}
				if got := console.Width(l[:bi]); got != iconCol {
					t.Fatalf("子项 %q 分支起点显示列 %d 应与图标列 %d 对齐: %q", item, got, iconCol, l)
				}
			}
		}
	}

	// 子项冒号对齐：同一节内各子项冒号显示列一致（像父级一样）
	colonOf := func(item string) int {
		for _, l := range strings.Split(out, "\n") {
			if i := strings.Index(l, item); i >= 0 {
				if ci := strings.Index(l[i:], ":"); ci >= 0 {
					return console.Width(l[:i+ci])
				}
			}
		}
		return -1
	}
	if toolsA, toolsB := colonOf("calculator"), colonOf("fetch_url"); toolsA != toolsB {
		t.Fatalf("工具子项冒号未对齐: %d vs %d", toolsA, toolsB)
	}
	if skillsA, skillsB := colonOf("report-sop"), colonOf("data-check"); skillsA != skillsB {
		t.Fatalf("技能子项冒号未对齐: %d vs %d", skillsA, skillsB)
	}
}

func TestPrintInventoryDisabled(t *testing.T) {
	var buf bytes.Buffer
	PrintInventory(&buf, Info{})
	out := buf.String()
	for _, want := range []string{
		": 未启用（MCP_MODE 未设置）",
		": 未启用（内存会话，单机）",
		": 未启用（ZEBRA_SHADOW_MODEL 未设置）",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("未启用状态缺少 %q\n%s", want, out)
		}
	}
}

// TestRedactURL：带凭据的 URL（REDIS_URL / WEBHOOK_URL）打印清单时
// 必须剥掉 userinfo，不泄露密码。
func TestRedactURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"redis://:secret@127.0.0.1:6379/0", "redis://127.0.0.1:6379/0"},
		{"redis://user:pwd@127.0.0.1:6379", "redis://127.0.0.1:6379"},
		{"127.0.0.1:6379", "127.0.0.1:6379"}, // 非 URL 原样返回
		{"redis://127.0.0.1:6379", "redis://127.0.0.1:6379"},
	}
	for _, c := range cases {
		if got := redactURL(c.in); got != c.want {
			t.Fatalf("redactURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPrintInventoryModeRow Zebra CLI 模式行并入清单树末行：
// 图标列（4）与冒号列（16）必须和上面各父行完全对齐。
func TestPrintInventoryModeRow(t *testing.T) {
	var buf bytes.Buffer
	PrintInventory(&buf, Info{Mode: "Chat 普通对话（输入 /mode 切换，/help 查看全部）"})
	out := buf.String()

	var modeLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "◇ 模式") {
			modeLine = l
			break
		}
	}
	if modeLine == "" {
		t.Fatalf("缺少模式行:\n%s", out)
	}
	if !strings.HasPrefix(modeLine, "└── ") {
		t.Fatalf("模式应为树末行（└──）: %q", modeLine)
	}
	iconIdx := strings.Index(modeLine, "◇")
	colonIdx := strings.Index(modeLine, ":")
	if got := console.Width(modeLine[:iconIdx]); got != 4 {
		t.Fatalf("模式图标显示列 %d 应为 4: %q", got, modeLine)
	}
	if got := console.Width(modeLine[:colonIdx]); got != 16 {
		t.Fatalf("模式冒号显示列 %d 应为 16: %q", got, modeLine)
	}
}
