package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// scriptedProvider 按提示词内容返回不同结果（反思/择优/普通采样）。
type scriptedProvider struct{ calls int }

func (s *scriptedProvider) Name() string { return "scripted" }
func (s *scriptedProvider) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	content := msgs[len(msgs)-1].Content
	switch {
	case strings.Contains(content, "评审员"):
		return provider.Message{Content: `{"revised":"改进版回答","reason":"更准确"}`}, nil
	case strings.Contains(content, "多个候选"):
		return provider.Message{Content: `{"answer":"选中回答"}`}, nil
	default:
		s.calls++
		return provider.Message{Content: fmt.Sprintf("候选%d", s.calls)}, nil
	}
}
func (s *scriptedProvider) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func newReflectAgent(p provider.Provider) *Agent {
	ag := New(Config{Router: provider.NewRouter(p), MaxTurns: 1})
	hist := make([]provider.Message, 0)
	return ag.Bind("s", "admin", "u", &hist)
}

func TestReflect(t *testing.T) {
	ag := newReflectAgent(&scriptedProvider{})
	got, err := ag.Reflect(context.Background(), "北京天气", "原回答")
	if err != nil {
		t.Fatal(err)
	}
	if got != "改进版回答" {
		t.Fatalf("反思应返回改进版，实际 %q", got)
	}
}

func TestReflectFallback(t *testing.T) {
	// 评审模型返回非 JSON → 回退原回答（质量兜底）
	ag := newReflectAgent(&fakeProvider{})
	got, err := ag.Reflect(context.Background(), "问题", "原回答")
	if err != nil || got != "原回答" {
		t.Fatalf("反思失败应回退原回答: %q %v", got, err)
	}
}

func TestSelfConsistent(t *testing.T) {
	ag := newReflectAgent(&scriptedProvider{})
	got, err := ag.SelfConsistent(context.Background(), "问题", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != "选中回答" {
		t.Fatalf("自一致性应择优，实际 %q", got)
	}
	// 六10：自一致性轮次必须写进会话历史（否则下一轮读不到上下文）
	if h := *ag.history; len(h) != 2 || h[0].Role != "user" || h[0].Content != "问题" || h[1].Content != "选中回答" {
		t.Fatalf("自一致性应记入历史，实际 %+v", h)
	}
}

func TestSelfConsistentSingle(t *testing.T) {
	ag := newReflectAgent(&scriptedProvider{})
	got, err := ag.SelfConsistent(context.Background(), "问题", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != "候选1" {
		t.Fatalf("单样本应直接返回回答，实际 %q", got)
	}
	// 六10：单样本路径同样记历史
	if h := *ag.history; len(h) != 2 || h[1].Content != "候选1" {
		t.Fatalf("单样本自一致性应记入历史，实际 %+v", h)
	}
}

func TestSelfConsistentFallback(t *testing.T) {
	// 择优输出不合规 → 回退第一个候选（fakeProvider 固定返回 "ok"）
	ag := newReflectAgent(&fakeProvider{})
	got, err := ag.SelfConsistent(context.Background(), "问题", 3)
	if err != nil || got != "ok" {
		t.Fatalf("择优失败应回退首个候选: %q %v", got, err)
	}
}
