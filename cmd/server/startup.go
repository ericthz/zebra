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
	fmt.Fprintln(w, "🧭 zebra 启动清单")

	// 1. 模型
	if len(info.Models) > 0 {
		fmt.Fprintf(w, "├── ⚙️  模型      : %s\n", strings.Join(info.Models, " → "))
	}

	// 2. 工具（数量 + 名称，学习时一眼看到"能调用什么"）
	if info.Tools != nil {
		names := info.Tools.Names()
		fmt.Fprintf(w, "├── 🛠  工具      : %d 个\n", len(names))
		if len(names) > 0 {
			fmt.Fprintf(w, "│     %s\n", strings.Join(names, ", "))
		}
	}

	// 3. MCP 状态（是否支持 / 是否就绪）
	if info.MCPMode == "" {
		fmt.Fprintln(w, "├── 🔌 MCP       : 未启用（MCP_MODE 未设置）")
	} else {
		fmt.Fprintf(w, "├── 🔌 MCP       : 模式=%s · 已连接 %d 个工具\n", info.MCPMode, info.MCPCount)
	}

	// 4. 技能列表
	if len(info.Skills) == 0 {
		fmt.Fprintln(w, "├── 📚 技能      : 无（skills/ 目录为空或加载失败）")
	} else {
		names := make([]string, 0, len(info.Skills))
		for _, sk := range info.Skills {
			names = append(names, fmt.Sprintf("%s(%s)", sk.Name, sk.Description))
		}
		fmt.Fprintf(w, "├── 📚 技能      : %d 个 —— %s\n", len(info.Skills), strings.Join(names, ", "))
	}

	// 5. 记忆 / 知识库
	fmt.Fprintf(w, "├── 🧠 记忆      : %s\n", orDefault(info.MemMode, "工作记忆"))
	fmt.Fprintf(w, "├── 📖 知识库    : %d 篇文档 / %d 块\n", info.RAGDocs, info.RAGChunks)

	// 6. 语音 / 影子评测 / Redis
	if info.VoiceEnabled {
		fmt.Fprintln(w, "├── 🎙  语音      : 已启用（ASR/TTS）")
	} else {
		fmt.Fprintln(w, "├── 🎙  语音      : 未启用（VOICE_BASE_URL 未设置）")
	}
	if info.ShadowCandidate != "" {
		fmt.Fprintf(w, "├── 🧪 影子评测  : candidate=%s · sample=%.0f%%\n", info.ShadowCandidate, info.ShadowSample*100)
	} else {
		fmt.Fprintln(w, "├── 🧪 影子评测  : 未启用（ZEBRA_SHADOW_MODEL 未设置）")
	}
	if info.RedisURL != "" {
		fmt.Fprintf(w, "└── 🔴 Redis     : %s（会话共享）\n", info.RedisURL)
	} else {
		fmt.Fprintln(w, "└── 🔴 Redis     : 未启用（内存会话，单机）")
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
