// 用量上报：provider 层通过 ctx 把每次成功的 LLM 调用上报给上层。
//
// 背景：此前 OnUsage 只在 agent.run 末尾调用一次——react/plan/debate/
// consistent/supervisor/rewrite 等直接调 StructuredChat/ChatWithFallback 的
// 路径从不触发（成本=0）；多轮工具循环每轮重发全量上下文也只记一次；降级时
// 按主模型名计价（错归模型）。
//
// 方案：provider 层不依赖 agent（token 估算在 agent），因此这里只通过 ctx
// 上报"原始调用信息"（实际服务的模型名 + 入/出消息），由上层 Agent 包装 ctx
// 时用自家估算器折算 token 并触发 OnUsage。每条成功调用上报一次，天然覆盖
// 全部模式与每一轮工具循环，且模型名取实际服务者（降级/换主后计价正确）。
package provider

import "context"

// UsageCall 一次成功的 LLM 调用。
type UsageCall struct {
	Model string    // 实际服务的 provider 名（非主模型名，降级后计价正确）
	In    []Message // 发送的消息（含工具调用历史）
	Out   Message   // 模型返回的消息
}

type usageKey struct{}

// WithUsageCollector 把用量收集器挂进 ctx；nil fn 视为不采集。
func WithUsageCollector(ctx context.Context, fn func(UsageCall)) context.Context {
	return context.WithValue(ctx, usageKey{}, fn)
}

// reportUsage 上报一次成功调用；ctx 未挂收集器时静默跳过。
func ReportUsage(ctx context.Context, c UsageCall) {
	if fn, ok := ctx.Value(usageKey{}).(func(UsageCall)); ok && fn != nil {
		fn(c)
	}
}
