// 启动能力清单（P31）：服务就绪前打印一张"学习友好"的资产盘点表。
//
// 背景：启动日志此前是零散的 JSON（技能、MCP、语音各打一条），学习者
// 难以一眼看清"这台服务到底有什么"。本文件把关键资产聚合成一张终端
// 友好的清单：模型、工具（数量+名称）、MCP 状态、技能、记忆、知识库、
// 语音、影子评测、Redis。
package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/ericthz/zebra/internal/console"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// startupInfo 启动清单所需信息（由 main 装配后传入）。
type startupInfo struct {
	Models          []string       // 路由链上的模型名（主 + 备）
	Tools           *tool.Registry // 已注册工具（Names 排序输出）
	Skills          []*skill.Skill // 已加载技能（可能为空）
	MCPMode         string         // "stdio"/"http"/空
	MCPCount        int            // 已连接的 MCP 工具数
	MemMode         string         // 记忆模式说明（如 "工作记忆 + Qdrant"）
	RAGDocs         int            // 已摄入文档数
	RAGChunks       int            // 知识库分块数
	VoiceEnabled    bool
	ShadowCandidate string // 影子评测候选模型（空=关闭）
	ShadowSample    float64
	RedisURL        string // 空=内存会话
}

// printStartupInventory 打印能力清单（纯函数，便于测试与复用）。
func printStartupInventory(w io.Writer, info startupInfo) {
	fmt.Fprintln(w, console.Symbol("──", console.ColorTitle)+" zebra 启动清单")
	lbl := func(sym string, code int, name string) string { // 符号上色 + 标签列定宽
		return console.Pad(console.Symbol(sym, code)+" "+name, 16)
	}

	// 1. 模型
	if len(info.Models) > 0 {
		fmt.Fprintf(w, "├── %s: %s\n", lbl("◆", console.ColorModel, "模型"), strings.Join(info.Models, " → "))
	}

	// 2. 工具（数量 + 逗号连接的名称列表）
	if info.Tools != nil {
		names := info.Tools.Names()
		fmt.Fprintf(w, "├── %s: %d 个\n", lbl("▲", console.ColorTool, "工具"), len(names))
		if len(names) > 0 {
			fmt.Fprintf(w, "│     %s\n", strings.Join(names, ", "))
		}
	}

	// 3. MCP 状态（是否支持 / 是否就绪）
	if info.MCPMode == "" {
		fmt.Fprintf(w, "├── %s: 未启用（MCP_MODE 未设置）\n", lbl("●", console.ColorMCP, "MCP"))
	} else {
		fmt.Fprintf(w, "├── %s: 模式=%s · 已连接 %d 个工具\n", lbl("●", console.ColorMCP, "MCP"), info.MCPMode, info.MCPCount)
	}

	// 4. 技能列表（名称(描述) 逗号连接）
	if len(info.Skills) == 0 {
		fmt.Fprintf(w, "├── %s: 无（skills/ 目录为空或加载失败）\n", lbl("■", console.ColorSkill, "技能"))
	} else {
		names := make([]string, 0, len(info.Skills))
		for _, sk := range info.Skills {
			names = append(names, fmt.Sprintf("%s(%s)", sk.Name, sk.Description))
		}
		fmt.Fprintf(w, "├── %s: %d 个 —— %s\n", lbl("■", console.ColorSkill, "技能"), len(info.Skills), strings.Join(names, ", "))
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
