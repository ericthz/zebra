// Package agent Agent 核心引擎。
//
// 职责：编排 LLM（多模型路由）→ 工具调用循环 → 记忆（分层）→ 上下文工程，
// 并串联安全横切点（注入防护、内容审核、审计、高危确认）。
// 一个 Agent 实例绑定"一个会话"，会话共享底层只读组件（Router/Registry），
// 历史与记忆按会话隔离（A4 多租户隔离）。
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/rag"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// Config Agent 构造参数。
type Config struct {
	Router       *provider.Router                                                   // C15 多模型路由（含 fallback）
	Tools        *tool.Registry                                                     // D20 工具权限白名单所在
	Prompts      *prompt.Registry                                                   // C16 系统提示模板
	Mem          *memory.Manager                                                    // C12 分层记忆
	Window       *ContextWindow                                                     // C11 上下文工程（nil 则关闭预算控制）
	Moderator    safety.Moderator                                                   // D18 内容审核（nil 则跳过）
	MaxTurns     int                                                                // 工具调用最大轮数
	PromptName   string                                                             // 使用的系统提示模板名
	Model        string                                                             // 当前模型名（用于成本归因 P5）
	OnUsage      func(model string, inTokens, outTokens int)                        // B5/P5 用量与成本钩子
	Skills       *skill.Registry                                                    // 技能注册表（P1，nil 则关闭技能检索）
	Cache        *cache.SemanticCache                                               // 语义缓存（P5，nil 则关闭）
	RAG          *rag.Index                                                         // 知识库检索（P8，nil 则关闭 RAG）
	Profile      *memory.ProfileStore                                               // 用户画像（P22，nil 则关闭）
	ProfileTTL   time.Duration                                                      // 画像事实保鲜期（P22，<=0 永不过期）
	Extractor    memory.Extractor                                                   // 画像抽取器（P27，nil 用规则抽取）
	OnSkill      func(names []string)                                               // 技能注入钩子（P31，nil 则关闭）
	OnTool       func(name string, args map[string]interface{}, ok bool, err error) // 工具调用钩子（P31）
	RewriteQuery bool                                                               // 查询改写（P48，false 则关闭）
}

// Agent 单个会话的 Agent 实例。
type Agent struct {
	cfg       Config
	history   *[]provider.Message // 指向会话层持有的历史（共享、隔离）
	sessionID string
	user      string
	role      string
}

// New 构造 Agent。
func New(cfg Config) *Agent {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 5
	}
	return &Agent{cfg: cfg}
}

// Bind 绑定会话上下文：共享的历史切片 + 会话 ID + 用户 + 角色（A4 隔离关键）。
func (a *Agent) Bind(sessionID, role, user string, history *[]provider.Message) *Agent {
	a.sessionID = sessionID
	a.role = role
	a.user = user
	a.history = history
	return a
}

// RunOptions 单次运行的横切参数。
type RunOptions struct {
	Confirm func(toolName string, args map[string]interface{}) bool // D20 高危工具二次确认
}

// Run 执行一轮对话（非流式），返回最终回复。
func (a *Agent) Run(ctx context.Context, userInput string, opts RunOptions) (string, error) {
	return a.run(ctx, userInput, opts, nil)
}

// run 核心循环。emit 非 nil 时把增量事件推给调用方（流式模式）。
func (a *Agent) run(ctx context.Context, userInput string, opts RunOptions, emit func(Event)) (string, error) {
	// P5 语义缓存：命中相似历史问答 → 直接返回，省一次 LLM 调用（省钱省延迟）。
	// 仅对"纯文本回答"缓存（toolLoop 返回 toolsUsed=false 时才写入）。
	if a.cfg.Cache != nil && emit == nil {
		if answer, ok := a.cfg.Cache.Get(ctx, userInput); ok {
			return answer, nil
		}
	}

	if a.cfg.Moderator != nil { // D18 输入审核
		if ok, reason := a.cfg.Moderator.Check(userInput); !ok {
			return "", fmt.Errorf("输入未通过内容审核: %s", reason)
		}
	}

	// 1. 组装消息（记忆 + 历史 + 系统提示 + 当前输入），并做上下文预算裁剪（C11）
	msgs := a.buildMessages(ctx, userInput)

	// 2. 工具调用循环（抽取为 toolLoop，供规划-执行 P10 复用）
	finalAnswer, lastErr, toolsUsed := a.toolLoop(ctx, msgs, a.cfg.Tools.ToolsFor(a.role), opts, emit)
	if lastErr != nil && finalAnswer == "" {
		return "", lastErr
	}

	// 3. 更新会话历史（只保留 user/assistant，压缩中间工具细节）
	if a.history != nil {
		*a.history = append(*a.history,
			provider.Message{Role: "user", Content: userInput},
			provider.Message{Role: "assistant", Content: finalAnswer},
		)
	}

	// 4. 记忆（C12 分层）
	if a.cfg.Mem != nil {
		a.cfg.Mem.Remember(a.sessionID, userInput, finalAnswer)
	}

	// 4.1 画像学习（P22/P27）：从用户输入抽取事实入库。
	// 只从用户输入抽取（模型回答含事实的置信度低）。P27 起支持
	// LLM 语义抽取，失败自动回退规则抽取（Extractor 接口可替换）。
	if a.cfg.Profile != nil {
		extractor := a.cfg.Extractor
		if extractor == nil {
			extractor = memory.RuleExtractor{}
		}
		if facts, err := extractor.Extract(ctx, userInput); err == nil && len(facts) > 0 {
			a.cfg.Profile.Learn(a.user, facts, time.Now())
		}
	}

	// 5. 输出审核（D18）
	if a.cfg.Moderator != nil {
		if ok, reason := a.cfg.Moderator.Check(finalAnswer); !ok {
			return "", fmt.Errorf("输出未通过内容审核: %s", reason)
		}
	}

	// 6. 用量与成本指标（B5/P5）：以估算值上报，生产用精确计费
	if a.cfg.OnUsage != nil {
		inTokens := 0
		for _, m := range msgs {
			inTokens += MessageTokens(m)
		}
		a.cfg.OnUsage(a.cfg.Model, inTokens, EstimateTokens(finalAnswer))
	}

	// 7. 写入语义缓存（P5）：仅纯文本回答（未调工具）才缓存
	if a.cfg.Cache != nil && !toolsUsed && finalAnswer != "" {
		a.cfg.Cache.Put(ctx, userInput, finalAnswer)
	}
	return finalAnswer, nil
}

// toolLoop 核心工具调用循环（P10 复用单元）：
// 反复"调 LLM → 若有工具调用则执行 → 再调"直到出纯文本或达到轮数上限。
// 返回：最终回答、错误、是否调用过工具（供缓存/统计判断）。
// 注意：本函数【不】写历史/记忆 —— 由调用方决定（Run 会写，规划-执行的
// 子步骤不写，避免中间过程污染对话）。
// callProvider 调用 LLM：非流式走路由 fallback；流式优先主模型，失败自动降级非流式。
func (a *Agent) callProvider(ctx context.Context, msgs []provider.Message, tools []provider.Tool, emit func(Event)) (provider.Message, error) {
	if emit == nil {
		msg, _, err := a.cfg.Router.ChatWithFallback(ctx, msgs, tools)
		return msg, err
	}
	// 流式：主模型
	primary := a.cfg.Router.Primary()
	if primary != nil {
		ch, err := primary.ChatStream(ctx, msgs, tools)
		if err == nil {
			msg, cerr := collectStream(ch, emit)
			if cerr == nil {
				return msg, nil
			}
		}
	}
	// 降级：非流式 fallback（B7 降级）
	msg, _, err := a.cfg.Router.ChatWithFallback(ctx, msgs, tools)
	return msg, err
}

func (a *Agent) toolLoop(ctx context.Context, msgs []provider.Message, tools []provider.Tool, opts RunOptions, emit func(Event)) (string, error, bool) {
	var finalAnswer string
	var lastErr error
	toolsUsed := false // 是否调用过工具

	// 循环检测（B9 健壮性）：连续相同工具调用视为死循环，提前中止。
	// 小模型容易出现"反复调同一工具不产出"的退化行为。
	prevCallsKey := ""
	repeatTurns := 0
	const maxRepeatTurns = 3

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		select {
		case <-ctx.Done():
			return finalAnswer, ctx.Err(), toolsUsed // B9 上下文取消/超时传播
		default:
		}

		respMsg, err := a.callProvider(ctx, msgs, tools, emit)
		if err != nil {
			lastErr = err // 错误由调用方统一处理，避免流式双发
			break
		}
		msgs = append(msgs, respMsg)

		if len(respMsg.ToolCalls) == 0 {
			finalAnswer = respMsg.Content
			lastErr = nil
			break
		}

		// 循环检测：本轮工具调用组合与上一轮完全相同 → 无进展
		callsKey := ""
		for _, tc := range respMsg.ToolCalls {
			callsKey += tc.Function.Name + "|" + string(tc.Function.Arguments) + ";"
		}
		if callsKey == prevCallsKey {
			repeatTurns++
		} else {
			prevCallsKey = callsKey
			repeatTurns = 0
		}
		if repeatTurns >= maxRepeatTurns {
			lastErr = fmt.Errorf("模型反复调用相同工具，疑似死循环，已中止")
			break
		}

		// 并行执行工具调用（P9 fan-out / fan-in）：
		// 同一轮多个工具互不依赖，并发执行可大幅降低总延迟。
		// 用带索引的 results 保证结果按调用顺序回填，对话不失序。
		toolsUsed = true
		results := make([]provider.Message, len(respMsg.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range respMsg.ToolCalls {
			wg.Add(1)
			go func(i int, tc provider.ToolCall) {
				defer wg.Done()
				results[i] = a.execTool(ctx, tc, opts, emit)
			}(i, tc)
		}
		wg.Wait()
		msgs = append(msgs, results...)
	}

	if finalAnswer == "" && lastErr == nil {
		lastErr = fmt.Errorf("达到最大工具调用轮数 %d，对话未完成", a.cfg.MaxTurns)
	}
	return finalAnswer, lastErr, toolsUsed
}

// execTool 单次工具调用的完整处理链（P9 并行执行的最小执行单元）：
//
//	解析参数(C13纠错) → 高危二次确认(D20) → 执行 → 注入防护(D17) → 事件上报
//
// 返回要追加进对话的工具结果消息。
func (a *Agent) execTool(ctx context.Context, tc provider.ToolCall, opts RunOptions, emit func(Event)) provider.Message {
	args, perr := tool.ParseArguments(tc.Function.Arguments)
	if perr != nil {
		// C13 纠错：参数解析失败直接作为错误反馈给模型重试
		return a.toolResult(tc, fmt.Sprintf("参数解析错误: %v", perr), true)
	}

	// D20 高危二次确认：拒绝则把"用户拒绝"反馈给模型，不让其再次尝试
	if opts.Confirm != nil && !opts.Confirm(tc.Function.Name, args) {
		return a.toolResult(tc, "用户拒绝执行该工具调用，请勿重试并改用其他方式回答", true)
	}

	result, terr := a.cfg.Tools.Execute(ctx, tc.Function.Name, args, a.user, a.role, opts.Confirm != nil)
	if a.cfg.OnTool != nil { // P31：上报一次真实工具执行（参数由调用方脱敏）
		a.cfg.OnTool(tc.Function.Name, args, terr == nil, terr)
	}
	if terr != nil {
		result = fmt.Sprintf("工具执行错误: %v", terr)
	}
	if emit != nil {
		emit(Event{Type: EventTool, Name: tc.Function.Name, Args: args})
	}
	return a.toolResult(tc, result, false)
}

func (a *Agent) toolResult(tc provider.ToolCall, content string, isErr bool) provider.Message {
	if !isErr {
		// D17 注入防护：外部工具结果包上隔离标记
		content = safety.SanitizeToolResult(content)
	}
	return provider.Message{Role: "tool", Content: content, ToolCallID: tc.ID, Name: tc.Function.Name}
}

// buildMessages 组装发送给模型的完整消息：系统提示 + 记忆 + 历史 + 当前输入。
func (a *Agent) buildMessages(ctx context.Context, userInput string) []provider.Message {
	// 查询改写（P48）：先改写问题，再用于技能/RAG 检索与最终消息
	if a.cfg.RewriteQuery {
		userInput = a.rewriteForRetrieval(ctx, userInput)
	}
	var msgs []provider.Message

	// 系统提示（C16 模板渲染，含角色信息）
	if sys, err := a.cfg.Prompts.Render(a.cfg.PromptName, map[string]string{"role": a.role}); err == nil {
		msgs = append(msgs, provider.Message{Role: "system", Content: sys})
	}

	// 记忆检索（C12）
	if a.cfg.Mem != nil {
		if items := a.cfg.Mem.Recall(ctx, a.sessionID, userInput, 3); len(items) > 0 {
			msgs = append(msgs, provider.Message{Role: "system", Content: "相关记忆：\n" + strings.Join(items, "\n")})
		}
	}

	// 用户画像注入（P22）：把已学到的用户事实作为 system 消息带给模型，
	// 让回答"记得"用户偏好（少问一遍）；TTL 之外的事实自动不参与注入。
	if a.cfg.Profile != nil {
		if facts := a.cfg.Profile.FactsFor(a.user, time.Now(), a.cfg.ProfileTTL); len(facts) > 0 {
			var pf strings.Builder
			pf.WriteString("以下是该用户的已知画像事实（回答时自然运用，不要复述给用户）：\n")
			for _, f := range facts {
				fmt.Fprintf(&pf, "- %s: %s\n", f.Key, f.Value)
			}
			msgs = append(msgs, provider.Message{Role: "system", Content: pf.String()})
		}
	}

	// 技能检索与注入（P1 技能体系）：
	// 按用户输入在技能库里命中相关技能（懒加载，只注入命中的，避免全量塞上下文），
	// 把技能的 SOP 指令作为 system 消息告诉模型"遇到这类任务请按以下步骤执行"。
	// 生产演化：技能检索换向量匹配；命中技能后可进一步按需读取其 scripts/ 资源。
	if a.cfg.Skills != nil {
		if hits := a.cfg.Skills.Match(userInput, 2); len(hits) > 0 {
			if a.cfg.OnSkill != nil { // P31：对外上报"本次注入了哪些技能"（学习/可观测）
				names := make([]string, 0, len(hits))
				for _, sk := range hits {
					names = append(names, sk.Name)
				}
				a.cfg.OnSkill(names)
			}
			for _, sk := range hits {
				msgs = append(msgs, provider.Message{
					Role:    "system",
					Content: fmt.Sprintf("【已启用技能 %s v%s：%s】\n%s", sk.Name, sk.Version, sk.Description, sk.Instructions),
				})
			}
		}
	}

	// RAG 知识库检索与注入（P8 / P20 混合检索）：
	// 按用户输入在私有知识库检索 topK 相关片段，作为 system 消息注入。
	// P20 起默认用【向量+BM25 混合检索】：向量抓语义、BM25 抓精确术语
	// （专有名词/编号类问题不再因嵌入质量丢分），片段带【来源】标记
	// 要求模型基于资料回答（接地/防幻觉，支持引用）。
	if a.cfg.RAG != nil {
		if hits, err := a.cfg.RAG.RetrieveHybrid(ctx, userInput, 3, 0.7); err == nil && len(hits) > 0 {
			var kb strings.Builder
			kb.WriteString("以下是知识库中与本问题相关的资料（回答请优先基于这些资料，并标注来源）：\n")
			for _, h := range hits {
				fmt.Fprintf(&kb, "【来源:%s#%d】(相关度%.2f)\n%s\n\n", h.Chunk.Source, h.Chunk.Seq, h.Score, h.Chunk.Text)
			}
			msgs = append(msgs, provider.Message{Role: "system", Content: kb.String()})
		}
	}

	// 历史（仅 user/assistant）
	if a.history != nil {
		for _, m := range *a.history {
			if m.Role == "user" || m.Role == "assistant" {
				msgs = append(msgs, m)
			}
		}
	}

	msgs = append(msgs, provider.Message{Role: "user", Content: userInput})

	// 上下文预算裁剪（C11）
	if a.cfg.Window != nil {
		msgs = a.cfg.Window.Trim(msgs)
	}
	return msgs
}
