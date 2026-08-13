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
		Title:           "zebra 启动清单",
		Models:          []string{"qwen3.5:0.8b-mlx", "gpt-4o-mini"},
		Tools:           reg,
		Skills:          []*skill.Skill{{Name: "report-sop", Description: "写研究报告"}, {Name: "data-check", Description: "数据核对"}},
		MCPMode:         "http",
		MCPCount:        3,
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
		"zebra 启动清单",
		": 2 个",
		"calculator: test", // 工具子项：名称: 描述（与父级冒号风格一致）
		"fetch_url: test",
		"模式=http · 已连接 3 个工具",
		"技能",
		"report-sop: 写研究报告",
		"data-check: 数据核对",
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
