// Package observe 启动能力清单（P31/P36）：zebra CLI 与 server 共用的清单渲染。
//
// 背景：两个入口此前各自打印清单，行结构与状态措辞容易漂移。抽成共享
// 渲染器后，两端只用同一套符号、配色与行格式；"这台程序有什么"一目了然，
// 且每类能力的状态由各自装配后如实传入。
package observe

import (
	"fmt"
	"io"
	"strings"

	"github.com/ericthz/zebra/internal/console"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// labelWidth 标签列定宽（显示宽度）。
const labelWidth = 16

// childIndent 子项缩进：父行 "├── "(4 格) + 标签(labelWidth) 后冒号位于第
// 4+labelWidth+1 列；子项用 "│" + (3+labelWidth) 个空格，使内容起点与冒号同列。
var childIndent = "│" + strings.Repeat(" ", 3+labelWidth)

// Info 启动清单所需信息（由 cmd/zebra、cmd/server 装配后传入）。
type Info struct {
	Title           string         // 清单标题（如 "zebra 启动清单" / "zebra CLI Agent（输入 exit 退出）"）
	Models          []string       // 路由链上的模型名（主 + 备）
	Tools           *tool.Registry // 已注册工具（Names 排序输出）
	Skills          []*skill.Skill // 已加载技能（可能为空）
	MCPMode         string         // "stdio"/"http"/空
	MCPCount        int            // 已连接的 MCP 工具数
	MemMode         string         // 记忆模式说明（如 "工作记忆 + Qdrant"）
	RAGDocs         int            // 已摄入文档数
	RAGChunks       int            // 知识库分块数
	VoiceEnabled    bool           // 语音客户端是否已装配
	ShadowCandidate string         // 影子评测候选模型（空=未启用）
	ShadowSample    float64
	RedisURL        string // 空=内存会话
}

// PrintInventory 打印能力清单（纯函数，便于测试与两端复用）。
func PrintInventory(w io.Writer, info Info) {
	fmt.Fprintln(w, console.Symbol("──", console.ColorTitle)+" "+orDefault(info.Title, "zebra 启动清单"))
	lbl := func(sym string, code int, name string) string { // 符号上色 + 标签列定宽
		return console.Pad(console.Symbol(sym, code)+" "+name, labelWidth)
	}

	// 1. 模型
	if len(info.Models) > 0 {
		fmt.Fprintf(w, "├── %s: %s\n", lbl("◆", console.ColorModel, "模型"), strings.Join(info.Models, " → "))
	}

	// 2. 工具（父级：数量；子项：每个工具逐行，起点与冒号对齐）
	if info.Tools != nil {
		names := info.Tools.Names()
		fmt.Fprintf(w, "├── %s: %d 个\n", lbl("▲", console.ColorTool, "工具"), len(names))
		if len(names) > 0 {
			for _, n := range names {
				fmt.Fprintf(w, "%s%s\n", childIndent, n)
			}
		}
	}

	// 3. MCP 状态（是否支持 / 是否就绪）
	if info.MCPMode == "" {
		fmt.Fprintf(w, "├── %s: 未启用（MCP_MODE 未设置）\n", lbl("●", console.ColorMCP, "MCP"))
	} else {
		fmt.Fprintf(w, "├── %s: 模式=%s · 已连接 %d 个工具\n", lbl("●", console.ColorMCP, "MCP"), info.MCPMode, info.MCPCount)
	}

	// 4. 技能（父级：数量；子项：每个技能名称(描述) 逐行，起点与冒号对齐）
	if len(info.Skills) == 0 {
		fmt.Fprintf(w, "├── %s: 无（skills/ 目录为空或加载失败）\n", lbl("■", console.ColorSkill, "技能"))
	} else {
		fmt.Fprintf(w, "├── %s: %d 个\n", lbl("■", console.ColorSkill, "技能"), len(info.Skills))
		for _, sk := range info.Skills {
			fmt.Fprintf(w, "%s%s(%s)\n", childIndent, sk.Name, sk.Description)
		}
	}

	// 5. 记忆 / 知识库
	fmt.Fprintf(w, "├── %s: %s\n", lbl("▣", console.ColorMemory, "记忆"), orDefault(info.MemMode, "工作记忆"))
	fmt.Fprintf(w, "├── %s: %d 篇文档 / %d 块\n", lbl("▤", console.ColorKB, "知识库"), info.RAGDocs, info.RAGChunks)

	// 6. 语音 / 影子评测 / Redis
	if info.VoiceEnabled {
		fmt.Fprintf(w, "├── %s: 已启用（ASR/TTS）\n", lbl("♪", console.ColorVoice, "语音"))
	} else {
		fmt.Fprintf(w, "├── %s: 未启用（VOICE_BASE_URL 未设置）\n", lbl("♪", console.ColorVoice, "语音"))
	}
	if info.ShadowCandidate != "" {
		fmt.Fprintf(w, "├── %s: candidate=%s · sample=%.0f%%\n", lbl("◐", console.ColorShadow, "影子评测"), info.ShadowCandidate, info.ShadowSample*100)
	} else {
		fmt.Fprintf(w, "├── %s: 未启用（ZEBRA_SHADOW_MODEL 未设置）\n", lbl("◐", console.ColorShadow, "影子评测"))
	}
	if info.RedisURL != "" {
		fmt.Fprintf(w, "└── %s: %s（会话共享）\n", lbl("◎", console.ColorRedis, "Redis"), info.RedisURL)
	} else {
		fmt.Fprintf(w, "└── %s: 未启用（内存会话，单机）\n", lbl("◎", console.ColorRedis, "Redis"))
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
