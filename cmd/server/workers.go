// 多 Agent 专业 Worker 构建（P13）。
//
// 三个专业 Agent 共享底层依赖（router/记忆/技能/RAG…），但各有：
//   - 专属系统提示词（persona）：数据 / 知识 / 常规
//   - 专属工具子集（tool.Subset）：能力聚焦
//
// Supervisor 负责把请求路由给最合适的 worker。
package main

import (
	"log/slog"

	"github.com/ericthz/zebra/internal/agent"
	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/cost"
	"github.com/ericthz/zebra/internal/kg"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/supervisor"
	"github.com/ericthz/zebra/internal/tool"
)

// workerDeps 构建专业 worker 所需的共享依赖。
type workerDeps struct {
	router    *provider.Router
	prompts   *prompt.Registry
	mem       *memory.Manager
	window    *agent.ContextWindow
	moderator safety.Moderator
	skills    *skill.Registry
	cache     *cache.SemanticCache
	rag       *rag.Index
	reranker  rag.Reranker
	kg        *kg.Graph
	model     string
	maxTurns  int
	cost      *cost.Tracker
	logger    *slog.Logger
}

// builder 生成"每请求新建"的 Agent 工厂（避免共享实例，保证并发安全）。
// promptName 决定使用哪个系统提示；reg 决定该 worker 可用的工具子集。
func (d workerDeps) builder(promptName string, reg *tool.Registry) func() *agent.Agent {
	return func() *agent.Agent {
		ag := agent.New(agent.Config{
			Router: d.router, Tools: reg, Prompts: d.prompts, Mem: d.mem,
			Window: d.window, Moderator: d.moderator, MaxTurns: d.maxTurns,
			PromptName: promptName, Skills: d.skills, Cache: d.cache, RAG: d.rag, Reranker: d.reranker, KG: d.kg,
			Model: d.model,
			OnUsage: func(m string, in, out int) { // P5 成本归因（按 worker 归组）
				if d.cost != nil {
					d.cost.Record("worker", "worker:"+promptName, m, in, out)
				}
			},
			OnInjection: func(kind, hit string) { // D17 注入检测（worker 侧同规格）
				d.logger.Warn("prompt.injection.detected", "worker", promptName, "kind", kind, "hit", hit)
			},
		})
		return ag
	}
}

// buildSupervisor 构建多 Agent 协调器（3 个专业 worker）。
func buildSupervisor(d workerDeps, allReg *tool.Registry) *supervisor.Supervisor {
	// 数据 worker：计算/换算/翻译/日期
	dataReg := allReg.Subset("calculator", "get_current_datetime", "generate_random_number",
		"convert_units", "translate_text")
	// 知识 worker：搜索/抓取/读文件
	knowReg := allReg.Subset("web_search", "fetch_url", "read_file", "list_dir")
	// 常规 worker：全量工具（含本地执行）
	genReg := allReg.Subset()

	return supervisor.NewSupervisor(d.router,
		&supervisor.Worker{
			Name:        "data",
			Description: "擅长计算、单位换算、翻译、日期时间等数据处理任务",
			Build:       d.builder("data", dataReg),
		},
		&supervisor.Worker{
			Name:        "knowledge",
			Description: "擅长搜索资料、抓取网页、查阅文档等知识获取任务",
			Build:       d.builder("knowledge", knowReg),
		},
		&supervisor.Worker{
			Name:        "general",
			Description: "通用助手，擅长综合问答、文件操作、代码等一般任务",
			Build:       d.builder("assistant", genReg),
		},
	)
}
