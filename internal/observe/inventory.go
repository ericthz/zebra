// Package observe 启动能力清单：Zebra CLI 与 server 共用的清单渲染。
//
// 背景：两个入口此前各自打印清单，行结构与状态措辞容易漂移。抽成共享
// 渲染器后，两端只用同一套符号、配色与行格式；"这台程序有什么"一目了然，
// 且每类能力的状态由各自装配后如实传入。
package observe

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/ericthz/zebra/internal/console"
	"github.com/ericthz/zebra/internal/mcp"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// redactURL 能力清单脱敏：剥掉 URL 里的 userinfo（REDIS_URL 常带
// 密码），避免清单/日志泄露凭据。解析失败原样返回。
func redactURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	parsed.User = nil
	return parsed.String()
}

// labelWidth 标签列定宽（显示宽度）：标题与内容的间距，保持紧凑。
const labelWidth = 12

// LabelWidth 清单标签列定宽（显示宽度），供清单外部行（如 CLI 模式行）对齐冒号。
const LabelWidth = labelWidth

// Label 生成清单行首标签：符号上色 + 标签列定宽（与 PrintInventory 内部一致）。
func Label(sym string, code int, name string) string {
	return console.Pad(console.Symbol(sym, code)+" "+name, labelWidth)
}

// childTrunkIcon 工具/技能子项树干：竖线定位到行首图标（▲/■）的正下方。
// 父行 "├── "(4 格) 后图标在第 5 列，故子项前缀 "│"(第 1 列树干) + 3 个空格，
// 分支起点在第 5 列，与图标垂直对齐。
var childTrunkIcon = "│" + strings.Repeat(" ", 3)

// Item 子项（如 MCP 工具）名称 + 描述。
type Item struct {
	Name        string
	Description string
}

// FromMCP 把 MCP 工具定义转为清单子项（供两端共用）。
func FromMCP(defs []mcp.ToolDef) []Item {
	if len(defs) == 0 {
		return nil
	}
	out := make([]Item, 0, len(defs))
	for _, d := range defs {
		out = append(out, Item{Name: d.Name, Description: d.Description})
	}
	return out
}

// branch 返回树形分支：非末项用 ├─，末项用 └─。
func branch(i, total int) string {
	if i == total-1 {
		return "└─ "
	}
	return "├─ "
}

// Info 启动清单所需信息（由 cmd/zebra、cmd/server 装配后传入）。
type Info struct {
	Title           string         // 清单标题（如 "Zebra 启动清单" / "Zebra CLI Agent"）
	Models          []string       // 路由链上的模型名（主 + 备）
	Tools           *tool.Registry // 已注册工具（Names 排序输出）
	Skills          []*skill.Skill // 已加载技能（可能为空）
	MCPMode         string         // "stdio"/"http"/空
	MCPCount        int            // 已连接的 MCP 工具数
	MCPTools        []Item         // MCP 已注册工具（名称+描述，供子项展示）
	MemMode         string         // 记忆模式说明（如 "工作记忆 + Qdrant"）
	RAGDocs         int            // 已摄入文档数
	RAGChunks       int            // 知识库分块数
	VoiceEnabled    bool           // 语音客户端是否已装配
	ShadowCandidate string         // 影子评测候选模型（空=未启用）
	ShadowSample    float64
	RedisURL        string // 空=内存会话
	Mode            string // 当前对话模式（空=不显示；Zebra CLI 传入，server 无全局模式）
	Compact         bool   // 精简模式：工具/MCP/技能只显示个数，子项不展开
	REPL            bool   // 交互式终端（有 /tools 等命令）：精简模式下个数行尾提示查看命令
}

// compactHint Compact 且 REPL 时返回行尾提示"（hint）"，否则空串。
// server 无 REPL 命令，精简时不给提示，避免误导。
func compactHint(info Info, hint string) string {
	if info.Compact && info.REPL {
		return "（" + hint + "）"
	}
	return ""
}

// PrintToolDetails 打印工具完整清单（名称 + 描述），供 REPL /tools 命令复用
// （tree=true 与 PrintInventory 展开时的树形子项一致；false 为 REPL 平铺靠左）。
func PrintToolDetails(w io.Writer, reg *tool.Registry, tree bool) {
	if reg == nil {
		return
	}
	names := reg.Names()
	descs := reg.Descriptions()
	maxName := 0
	for _, n := range names {
		if w := console.Width(n); w > maxName {
			maxName = w
		}
	}
	for i, n := range names {
		fmt.Fprintf(w, "%s%s: %s\n", indent(tree, i, len(names)), console.Pad(n, maxName+2), descs[n])
	}
}

// PrintMCPDetails 打印 MCP 已连接工具完整清单，供 REPL /mcp 命令复用。
func PrintMCPDetails(w io.Writer, tools []Item, tree bool) {
	if len(tools) == 0 {
		return
	}
	maxName := 0
	for _, t := range tools {
		if w := console.Width(t.Name); w > maxName {
			maxName = w
		}
	}
	for i, t := range tools {
		fmt.Fprintf(w, "%s%s: %s\n", indent(tree, i, len(tools)), console.Pad(t.Name, maxName+2), t.Description)
	}
}

// PrintSkillDetails 打印技能完整清单（名称 + 描述），供 REPL /skills 命令复用。
func PrintSkillDetails(w io.Writer, skills []*skill.Skill, tree bool) {
	if len(skills) == 0 {
		return
	}
	maxName := 0
	for _, sk := range skills {
		if w := console.Width(sk.Name); w > maxName {
			maxName = w
		}
	}
	for i, sk := range skills {
		fmt.Fprintf(w, "%s%s: %s\n", indent(tree, i, len(skills)), console.Pad(sk.Name, maxName+2), sk.Description)
	}
}

// indent 返回子项行前缀：树形（清单展开）用树干+分支竖线；平铺（REPL 命令）
// 去掉竖线与树形、不缩进，靠左对齐。
func indent(tree bool, i, total int) string {
	if !tree {
		return ""
	}
	return childTrunkIcon + branch(i, total)
}

// PrintInventory 打印能力清单（纯函数，便于测试与两端复用）。
func PrintInventory(w io.Writer, info Info) {
	fmt.Fprintln(w, console.Symbol("──", console.ColorTitle)+" "+orDefault(info.Title, "Zebra 启动清单"))
	lbl := func(sym string, code int, name string) string { // 符号上色 + 标签列定宽
		return Label(sym, code, name)
	}

	// 统计树总行数：模型/工具可选；模式可选且恒为末行（这样图标与冒号天然与前列对齐）
	total := 7 // MCP + 技能 + 记忆 + 知识库 + 语音 + 影子评测 + Redis
	if len(info.Models) > 0 {
		total++
	}
	if info.Tools != nil {
		total++
	}
	if info.Mode != "" {
		total++
	}
	row := 0
	parent := func() string { // 当前父行树前缀，随后行号 +1
		p := parentBranch(row, total)
		row++
		return p
	}

	// 1. 模型
	if len(info.Models) > 0 {
		fmt.Fprintf(w, "%s%s: %s\n", parent(), lbl("◆", console.ColorModel, "模型"), strings.Join(info.Models, " → "))
	}

	// 2. 工具（父级：数量；子项：树形分支逐行，名称 + 描述。Compact 只显示个数，REPL 同行提示）
	if info.Tools != nil {
		names := info.Tools.Names()
		fmt.Fprintf(w, "%s%s: %d 个%s\n", parent(), lbl("▲", console.ColorTool, "工具"), len(names), compactHint(info, "输入 /tools 查看全部"))
		if !info.Compact {
			PrintToolDetails(w, info.Tools, true)
		}
	}

	// 3. MCP 状态（父级：模式与连接数；子项：每个 MCP 工具逐行展示。Compact 只显示个数，REPL 同行提示）
	if info.MCPMode == "" {
		fmt.Fprintf(w, "%s%s: 未启用（MCP_MODE 未设置）\n", parent(), lbl("●", console.ColorMCP, "MCP"))
	} else {
		fmt.Fprintf(w, "%s%s: 模式=%s · 已连接 %d 个工具%s\n", parent(), lbl("●", console.ColorMCP, "MCP"), info.MCPMode, info.MCPCount, compactHint(info, "输入 /mcp 查看全部"))
		if !info.Compact {
			PrintMCPDetails(w, info.MCPTools, true)
		}
	}

	// 4. 技能（父级：数量；子项：树形分支逐行，名称 — 描述。Compact 只显示个数，REPL 同行提示）
	if len(info.Skills) == 0 {
		fmt.Fprintf(w, "%s%s: 无（skills/ 目录为空或加载失败）\n", parent(), lbl("■", console.ColorSkill, "技能"))
	} else {
		fmt.Fprintf(w, "%s%s: %d 个%s\n", parent(), lbl("■", console.ColorSkill, "技能"), len(info.Skills), compactHint(info, "输入 /skills 查看全部"))
		if !info.Compact {
			PrintSkillDetails(w, info.Skills, true)
		}
	}

	// 5. 记忆 / 知识库
	fmt.Fprintf(w, "%s%s: %s\n", parent(), lbl("▣", console.ColorMemory, "记忆"), orDefault(info.MemMode, "工作记忆"))
	fmt.Fprintf(w, "%s%s: %d 篇文档 / %d 块\n", parent(), lbl("▤", console.ColorKB, "知识库"), info.RAGDocs, info.RAGChunks)

	// 6. 语音 / 影子评测 / Redis
	if info.VoiceEnabled {
		fmt.Fprintf(w, "%s%s: 已启用（ASR/TTS）\n", parent(), lbl("♪", console.ColorVoice, "语音"))
	} else {
		fmt.Fprintf(w, "%s%s: 未启用（VOICE_BASE_URL 未设置）\n", parent(), lbl("♪", console.ColorVoice, "语音"))
	}
	if info.ShadowCandidate != "" {
		fmt.Fprintf(w, "%s%s: candidate=%s · sample=%.0f%%\n", parent(), lbl("◐", console.ColorShadow, "影子评测"), info.ShadowCandidate, info.ShadowSample*100)
	} else {
		fmt.Fprintf(w, "%s%s: 未启用（ZEBRA_SHADOW_MODEL 未设置）\n", parent(), lbl("◐", console.ColorShadow, "影子评测"))
	}
	if info.RedisURL != "" {
		fmt.Fprintf(w, "%s%s: %s（会话共享）\n", parent(), lbl("◎", console.ColorRedis, "Redis"), redactURL(info.RedisURL))
	} else {
		fmt.Fprintf(w, "%s%s: 未启用（内存会话，单机）\n", parent(), lbl("◎", console.ColorRedis, "Redis"))
	}
	// 7. 模式（可选末行：Zebra CLI 当前对话模式；图标/冒号与前列同一竖线）
	if info.Mode != "" {
		fmt.Fprintf(w, "%s%s: %s\n", parent(), lbl("◇", console.ColorModel, "模式"), info.Mode)
	}
}

// parentBranch 父级树前缀：非末项 ├──，末项 └──。
func parentBranch(i, total int) string {
	if i == total-1 {
		return "└── "
	}
	return "├── "
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
