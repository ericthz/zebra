package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/memory"
	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/tool"
)

// blockMarkerModerator 放行输入、拦截包含 marker 的内容（通用桩）。
type blockMarkerModerator struct{ marker string }

func (m blockMarkerModerator) Check(text string) (bool, string) {
	if strings.Contains(text, m.marker) {
		return false, "包含敏感词: " + m.marker
	}
	return true, ""
}

// newModeAgent 构造带审核器 + 工作记忆 + 历史的模式测试代理。
func newModeAgent(p provider.Provider, marker string) (*Agent, *[]provider.Message, *memory.WorkingMemory) {
	reg := tool.NewRegistry()
	reg.Register(&tool.CalculatorTool{})
	prompts := prompt.NewRegistry("z")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra 助手。"})
	hist := make([]provider.Message, 0)
	wm := memory.NewWorkingMemory(10)
	ag := New(Config{
		Router:     provider.NewRouter(p),
		Tools:      reg,
		Prompts:    prompts,
		Moderator:  blockMarkerModerator{marker: marker},
		Mem:        memory.NewManager(wm, nil),
		MaxTurns:   3,
		PromptName: "assistant",
	})
	ag.Bind("s", "admin", "u", &hist)
	return ag, &hist, wm
}

// assertBlocked 断言"未通过内容审核"错误且历史/记忆未写入。
func assertBlocked(t *testing.T, err error, hist []provider.Message, wm *memory.WorkingMemory) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "未通过内容审核") {
		t.Fatalf("应因内容审核失败返回错误，实际: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("被拦截内容不应写入会话历史，实际 %d 条: %+v", len(hist), hist)
	}
	if items := wm.Recent("s", 10); len(items) != 0 {
		t.Fatalf("被拦截内容不应写入工作记忆，实际: %+v", items)
	}
}

// ----：react / plan / debate / consistent 四种模式补输入+输出审核 ----

// TestReactModeration：ReAct 输入/输出都要过，违规不得持久化。
func TestReactModeration(t *testing.T) {
	// 输入违规：先于任何 LLM 调用拦截
	ag, hist, wm := newModeAgent(&reactProvider{}, "违规词")
	_, err := ag.ReAct(context.Background(), "违规词 1+2", RunOptions{}, 3)
	assertBlocked(t, err, *hist, wm)

	// 输出违规：模型产出违规答案 → 拦截且不持久化
	ag, hist, wm = newModeAgent(&blockedReactProvider{}, "赌博")
	_, err = ag.ReAct(context.Background(), "1+2", RunOptions{}, 3)
	assertBlocked(t, err, *hist, wm)
}

// blockedReactProvider 直接产出含违规词的答案（模拟模型输出违规内容）。
type blockedReactProvider struct{}

func (blockedReactProvider) Name() string { return "react-blocked" }
func (blockedReactProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: `{"thought":"已完成","answer":"赌博下注必胜"}`}, nil
}
func (blockedReactProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestPlanModeration：规划-执行输入/输出都要过。
func TestPlanModeration(t *testing.T) {
	// 输入违规
	ag, hist, wm := newModeAgent(&plannerProvider{}, "违规词")
	_, err := ag.PlanAndExecute(context.Background(), "违规词 复杂任务", RunOptions{})
	assertBlocked(t, err, *hist, wm)

	// 输出违规：汇总包含违规词 → 拦截（需在 rememberTurn 前，历史保持为空）
	ag, hist, wm = newModeAgent(&plannerProvider{}, "结果")
	_, err = ag.PlanAndExecute(context.Background(), "做一个复杂任务", RunOptions{})
	assertBlocked(t, err, *hist, wm)

	// 流式版输出审核同样生效
	ag, hist, wm = newModeAgent(&plannerProvider{}, "结果")
	var evs []Event
	_, err = ag.PlanAndExecuteStream(context.Background(), "做一个复杂任务", RunOptions{}, func(ev Event) { evs = append(evs, ev) })
	assertBlocked(t, err, *hist, wm)
}

// blockedDebateProvider 各方一律返回带敏感词的内容（左/右/最终立场均含违规内容）。
type blockedDebateProvider struct{}

func (blockedDebateProvider) Name() string { return "debate-blocked" }
func (blockedDebateProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: "赌博下注必胜"}, nil
}
func (blockedDebateProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestDebateModeration：辩论输入/输出都要过。
func TestDebateModeration(t *testing.T) {
	// 输入违规
	ag, hist, wm := newModeAgent(&blockedDebateProvider{}, "违规词")
	_, _, err := ag.Debate(context.Background(), "违规词 值得吗", "", "")
	assertBlocked(t, err, *hist, wm)

	// 输出违规：两辩手立场都违规 → 评审前左方立场也须拦截（回退路径同样审核）
	ag, hist, wm = newModeAgent(&blockedDebateProvider{}, "赌博")
	_, _, err = ag.Debate(context.Background(), "值得吗", "", "")
	assertBlocked(t, err, *hist, wm)
}

// TestConsistentModeration：自一致性输入/输出都要过。
func TestConsistentModeration(t *testing.T) {
	// 输入违规
	ag, hist, wm := newModeAgent(&scriptedProvider{}, "违规词")
	_, err := ag.SelfConsistent(context.Background(), "违规词 问题", 3)
	assertBlocked(t, err, *hist, wm)

	// 输出违规：择优结果含违规词 → 拦截且不持久化
	ag, hist, wm = newModeAgent(&blockedConsistentProvider{}, "赌博")
	_, err = ag.SelfConsistent(context.Background(), "问题", 3)
	assertBlocked(t, err, *hist, wm)
}

// blockedConsistentProvider 采样返回候选、择优返回违规内容。
type blockedConsistentProvider struct{}

func (blockedConsistentProvider) Name() string { return "consistent-blocked" }
func (blockedConsistentProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	if strings.Contains(msgs[len(msgs)-1].Content, "候选") {
		return provider.Message{Content: `{"answer":"赌博下注必胜"}`}, nil
	}
	return provider.Message{Content: "候选内容"}, nil
}
func (blockedConsistentProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// ----：reflect 修订版写回历史并重新审核 ----

// reflectToRevisedProvider 普通回答返回原回答，反思调用返回修订版。
type reflectToRevisedProvider struct{}

func (reflectToRevisedProvider) Name() string { return "reflect" }
func (reflectToRevisedProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	content := msgs[len(msgs)-1].Content
	if strings.Contains(content, "评审员") {
		return provider.Message{Content: `{"revised":"改进版回答","reason":"更准确"}`}, nil
	}
	return provider.Message{Content: "原回答"}, nil
}
func (reflectToRevisedProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// TestRunReflectWritesRevisedToHistory：修订版必须替换历史中的原回答
// 下一轮上下文读到的是改进后的回答。
func TestRunReflectWritesRevisedToHistory(t *testing.T) {
	ag, hist, wm := newModeAgent(&reflectToRevisedProvider{}, "绝不匹配的敏感词")
	out, err := ag.RunReflect(context.Background(), "北京天气", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "改进版回答" {
		t.Fatalf("RunReflect 应返回修订版，实际 %q", out)
	}
	h := *hist
	if len(h) != 2 || h[0].Content != "北京天气" || h[1].Content != "改进版回答" {
		t.Fatalf("历史应为（问题→修订版），实际 %+v", h)
	}
	// 工作记忆最近一条应被修订版替换（不再残留原回答）
	if items := wm.Recent("s", 10); len(items) != 1 || !strings.Contains(items[0], "改进版回答") || strings.Contains(items[0], "原回答") {
		t.Fatalf("记忆应被修订版替换，实际: %+v", items)
	}
}

// TestRunReflectModeratesRevised：修订版是最终交付物，必须重新过 ——
// 修订版违规时报错，历史不残留违规文本。
func TestRunReflectModeratesRevised(t *testing.T) {
	ag, hist, wm := newModeAgent(&reflectToRevisedProvider{}, "改进版")
	_, err := ag.RunReflect(context.Background(), "北京天气", RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "未通过内容审核") {
		t.Fatalf("修订版违规应报审核错误，实际: %v", err)
	}
	// 原回答（已过审核）保留在历史，违规修订版不得入库
	h := *hist
	if len(h) != 2 || h[1].Content != "原回答" {
		t.Fatalf("历史应保留已审核的原回答，实际 %+v", h)
	}
	if items := wm.Recent("s", 10); len(items) != 1 || !strings.Contains(items[0], "原回答") {
		t.Fatalf("记忆应保留原回答，实际: %+v", items)
	}
}
