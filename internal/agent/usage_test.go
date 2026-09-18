package agent

import (
	"context"
	"testing"
)

// TestUsageReportedPerCall：用量必须按"每次成功 LLM 调用"上报，且模型名
// 取实际服务者（不是固定主模型名）。
func TestUsageReportedPerCall(t *testing.T) {
	var calls []struct {
		model string
		in    int
		out   int
	}
	ag := newLoopAgent(&scriptedProvider{}, 3)
	ag.cfg.OnUsage = func(model string, in, out int) {
		calls = append(calls, struct {
			model string
			in    int
			out   int
		}{model, in, out})
	}

	// SelfConsistent 3 次采样 + 1 次择优 = 4 次成功调用
	if _, err := ag.SelfConsistent(context.Background(), "问题", 3); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("自一致性应上报 4 次用量（3 采样+1 择优），实际 %d: %+v", len(calls), calls)
	}
	for _, c := range calls {
		if c.model != "scripted" {
			t.Fatalf("模型名应取实际服务者 scripted，实际 %q", c.model)
		}
		if c.in <= 0 || c.out <= 0 {
			t.Fatalf("每次调用都应上报正 token 数: %+v", c)
		}
	}
}

// TestUsageToolLoopPerTurn：多轮工具循环每轮都要上报用量（此前只记一次）。
func TestUsageToolLoopPerTurn(t *testing.T) {
	var n int
	ag := newLoopAgent(&alternatingProvider{}, 4) // 会触发死循环中止
	ag.cfg.OnUsage = func(_ string, _, _ int) { n++ }

	_, _ = ag.Run(context.Background(), "问题", RunOptions{})
	if n == 0 {
		t.Fatal("工具循环应有用量上报")
	}
	// 死循环在 3 轮组合重复时中止：至少 3 次调用都上报
	if n < 3 {
		t.Fatalf("工具循环每轮都应上报，实际 %d 次", n)
	}
}

// TestUsageNotReportedWithoutHook：未挂 OnUsage 时静默跳过，不 panic。
func TestUsageNotReportedWithoutHook(t *testing.T) {
	ag := newLoopAgent(&scriptedProvider{}, 3)
	if _, err := ag.SelfConsistent(context.Background(), "问题", 3); err != nil {
		t.Fatal(err)
	}
}
