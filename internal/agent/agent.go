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

	"github.com/ericthz/zebra/internal/cache"
	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/safety"
	"github.com/ericthz/zebra/internal/skill"
	"github.com/ericthz/zebra/internal/tool"
)

// Config Agent 构造参数。
type Config struct {
	Router     *provider.Router    // C15 多模型路由（含 fallback）
	Tools      *tool.Registry      // D20 工具权限白名单所在
	Prompts    *prompt.Registry    // C16 系统提示模板
	Mem        *memory.Manager     // C12 分层记忆
	Window     *ContextWindow      // C11 上下文工程（nil 则关闭预算控制）
	Moderator  safety.Moderator    // D18 内容审核（nil 则跳过）
	MaxTurns   int                 // 工具调用最大轮数
	PromptName string              // 使用的系统提示模板名
	Model      string              // 当前模型名（用于成本归因 P5）
	OnUsage    func(model string, inTokens, outTokens int) // B5/P5 用量与成本钩子
	Skills     *skill.Registry     // 技能注册表（P1，nil 则关闭技能检索）
	Cache      *cache.SemanticCache // 语义缓存（P5，nil 则关闭）
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
	// 仅对"纯文本回答"缓存（本函数末尾 Put 时已过滤工具调用场景）。
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

	// 2. 工具调用循环
	tools := a.cfg.Tools.ToolsFor(a.role) // 只暴露该角色可见的工具（D20）
	var finalAnswer string
	var lastErr error
	toolsUsed := false // 是否调用过工具（调用了则不写入缓存，防结果过时）

	// 循环检测（B9 健壮性）：连续相同工具调用视为死循环，提前中止。
	// 小模型容易出现"反复调同一工具不产出"的退化行为。
	prevCallsKey := ""
	repeatTurns := 0
	const maxRepeatTurns = 3

	for turn := 0; turn < a.cfg.MaxTurns; turn++ {
		select {
		case <-ctx.Done():
			return finalAnswer, ctx.Err() // B9 上下文取消/超时传播
		default:
		}

		respMsg, err := a.callProvider(ctx, msgs, tools, emit)
		if err != nil {
			lastErr = err // 错误由调用方（Run/RunStream）统一处理，避免流式双发
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

		// 执行工具调用
		toolsUsed = true
		for _, tc := range respMsg.ToolCalls {
			args, perr := tool.ParseArguments(tc.Function.Arguments)
			if perr != nil {
				// C13 纠错：参数解析失败直接作为错误反馈给模型重试
				msgs = append(msgs, a.toolResult(tc, fmt.Sprintf("参数解析错误: %v", perr), true))
				continue
			}

			// D20 高危二次确认：拒绝则把"用户拒绝"反馈给模型，不让其再次尝试
			if opts.Confirm != nil && !opts.Confirm(tc.Function.Name, args) {
				msgs = append(msgs, a.toolResult(tc, "用户拒绝执行该工具调用，请勿重试并改用其他方式回答", true))
				continue
			}
			result, terr := a.cfg.Tools.Execute(ctx, tc.Function.Name, args, a.user, a.role, opts.Confirm != nil)
			if terr != nil {
				result = fmt.Sprintf("工具执行错误: %v", terr)
			}
			msgs = append(msgs, a.toolResult(tc, result, false))
			if emit != nil {
				emit(Event{Type: EventTool, Name: tc.Function.Name, Args: args})
			}
		}
	}

	if finalAnswer == "" && lastErr == nil {
		lastErr = fmt.Errorf("达到最大工具调用轮数 %d，对话未完成", a.cfg.MaxTurns)
	}
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

func (a *Agent) toolResult(tc provider.ToolCall, content string, isErr bool) provider.Message {
	if !isErr {
		// D17 注入防护：外部工具结果包上隔离标记
		content = safety.SanitizeToolResult(content)
	}
	return provider.Message{Role: "tool", Content: content, ToolCallID: tc.ID, Name: tc.Function.Name}
}

// buildMessages 组装发送给模型的完整消息：系统提示 + 记忆 + 历史 + 当前输入。
func (a *Agent) buildMessages(ctx context.Context, userInput string) []provider.Message {
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

	// 技能检索与注入（P1 技能体系）：
	// 按用户输入在技能库里命中相关技能（懒加载，只注入命中的，避免全量塞上下文），
	// 把技能的 SOP 指令作为 system 消息告诉模型"遇到这类任务请按以下步骤执行"。
	// 生产演化：技能检索换向量匹配；命中技能后可进一步按需读取其 scripts/ 资源。
	if a.cfg.Skills != nil {
		if hits := a.cfg.Skills.Match(userInput, 2); len(hits) > 0 {
			for _, sk := range hits {
				msgs = append(msgs, provider.Message{
					Role:    "system",
					Content: fmt.Sprintf("【已启用技能 %s v%s：%s】\n%s", sk.Name, sk.Version, sk.Description, sk.Instructions),
				})
			}
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
