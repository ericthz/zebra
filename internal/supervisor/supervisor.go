// Package supervisor 多 Agent 协作（P13）。
//
// 背景：单 Agent 用一套提示词/工具应对所有请求，能力不聚焦。成熟 Agent
// 采用【多 Agent 架构】：多个"专业 Agent"（数据/知识/写作…）各司其职，
// 由一个 Supervisor（协调者/路由器）把请求分发给最合适的那个。
//
// 本包实现最小 Supervisor：
//   - Worker：一个专业 Agent 的"构建工厂"（Build 返回每请求新建的 Agent，
//     避免共享可变状态；专业化体现在不同的系统提示词/工具子集）
//   - Route：先让 LLM 选 worker（JSON），失败/非法则关键词兜底
//   - Run：路由 → 构建 → 绑定会话 → 执行
//
// 生产演化方向：
//   - 更复杂的编排：Supervisor 可再调用子 Agent 并汇总（Orchestrator-Worker）
//   - 多 Agent 协作协议：子 Agent 之间互相传递结果（debate / handoff）
//   - 路由用向量/分类器，而非每次都调 LLM（省成本）
package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/provider"
)

// Worker 一个专业 Agent。
// Build 返回【每请求新建】的 Agent（不共享实例，保证并发安全与会话隔离）。
type Worker struct {
	Name        string // 唯一名（路由目标）
	Description string // 一句话说明擅长什么（路由与兜底用）
	Build       func() *agent.Agent
}

// Supervisor 多 Agent 协调器。
type Supervisor struct {
	router  *provider.Router // 用于路由判断的模型（可复用主模型）
	workers []*Worker
}

// NewSupervisor 构造。
func NewSupervisor(router *provider.Router, workers ...*Worker) *Supervisor {
	return &Supervisor{router: router, workers: workers}
}

// Workers 返回全部 worker 的只读元信息。
func (s *Supervisor) Workers() []*Worker { return s.workers }

// find 按名找 worker。
func (s *Supervisor) find(name string) *Worker {
	for _, w := range s.workers {
		if w.Name == name {
			return w
		}
	}
	return nil
}

// Route 选择最适合处理 query 的 worker。
// 先问 LLM（输出 JSON），失败或名字非法则关键词兜底（看 description 命中）。
func (s *Supervisor) Route(ctx context.Context, query string) (*Worker, error) {
	if len(s.workers) == 0 {
		return nil, fmt.Errorf("无可用 worker")
	}
	// 1. LLM 路由
	if s.router != nil {
		var sb strings.Builder
		sb.WriteString("你是任务分发器。从以下 Agent 中选一个最适合处理该请求的，只输出 JSON {\"worker\":\"名称\"}：\n")
		for _, w := range s.workers {
			fmt.Fprintf(&sb, "- %s: %s\n", w.Name, w.Description)
		}
		fmt.Fprintf(&sb, "用户请求：%s", query)

		msg, _, err := s.router.ChatWithFallback(ctx, []provider.Message{
			{Role: "user", Content: sb.String()},
		}, nil)
		if err == nil {
			if name := extractWorker(msg.Content); name != "" {
				if w := s.find(name); w != nil {
					return w, nil
				}
			}
		}
	}

	// 2. 关键词兜底：description 命中查询词最多的 worker
	return s.keywordFallback(query), nil
}

// extractWorker 从 LLM 回复中抽取 {"worker":"name"} 的值。
func extractWorker(content string) string {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start < 0 || end <= start {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(content[start:end+1]), &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m["worker"])
}

// keywordFallback 按 description 命中查询词的数量打分，选最高者；全 0 则第一个。
// 中文无空格，需按"逐字 + 英文整词"切分才能命中（与技能检索同策略）。
func (s *Supervisor) keywordFallback(query string) *Worker {
	tokens := cjkTokens(query)
	best, bestScore := s.workers[0], 0
	for _, w := range s.workers {
		score := 0
		desc := strings.ToLower(w.Description)
		for _, tok := range tokens {
			if strings.Contains(desc, tok) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = w, score
		}
	}
	return best
}

// cjkTokens 把查询切成检索词：中文逐字，英文按空格分词。
func cjkTokens(s string) []string {
	var tokens []string
	for _, field := range strings.Fields(strings.ToLower(s)) {
		isASCII := true
		for _, r := range field {
			if r >= 0x4e00 && r <= 0x9fff { // CJK 汉字
				tokens = append(tokens, string(r))
				isASCII = false
			}
		}
		if isASCII {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

// Run 路由并执行：选 worker → 新建 Agent → 绑定会话 → 运行。
func (s *Supervisor) Run(ctx context.Context, query, sessionID, role, user string, hist *[]provider.Message, opts agent.RunOptions) (string, *Worker, error) {
	w, err := s.Route(ctx, query)
	if err != nil {
		return "", nil, err
	}
	ag := w.Build()
	ag.Bind(sessionID, role, user, hist)
	reply, err := ag.Run(ctx, query, opts)
	return reply, w, err
}
