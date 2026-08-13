package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

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

func TestPrintStartupInventory(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(testTool{name: "calculator"})
	reg.Register(testTool{name: "fetch_url"})

	info := startupInfo{
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
	printStartupInventory(&buf, info)
	out := buf.String()
	for _, want := range []string{
		"zebra 启动清单",
		": 2 个",
		"calculator, fetch_url",
		"模式=http · 已连接 3 个工具",
		"技能",
		"report-sop(写研究报告)",
		": 2 篇文档 / 12 块",
		": 已启用（ASR/TTS）",
		"candidate=qwen2.5:7b",
		": 127.0.0.1:6379（会话共享）",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("启动清单缺少 %q\n%s", want, out)
		}
	}
}

func TestPrintStartupInventoryDisabled(t *testing.T) {
	var buf bytes.Buffer
	printStartupInventory(&buf, startupInfo{MCPMode: "", RedisURL: "", ShadowCandidate: ""})
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
