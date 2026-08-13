package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// fakeCandidate 固定回答的候选模型。
type fakeCandidate struct {
	reply string
	err   error
}

func (f *fakeCandidate) Name() string { return "fake-candidate" }
func (f *fakeCandidate) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	if f.err != nil {
		return provider.Message{}, f.err
	}
	return provider.Message{Role: "assistant", Content: f.reply}, nil
}
func (f *fakeCandidate) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// scriptedJudge 按"回答里是否出现候选标记"返回不同分数（模拟双评差异）。
type scriptedJudge struct{}

func (scriptedJudge) Name() string { return "scripted-judge" }
func (scriptedJudge) Chat(_ context.Context, msgs []provider.Message, _ []provider.Tool) (provider.Message, error) {
	last := msgs[len(msgs)-1].Content
	if strings.Contains(last, "候选回答") {
		return provider.Message{Content: `{"faithfulness":0.9,"relevance":0.9,"safety":0.9}`}, nil
	}
	return provider.Message{Content: `{"faithfulness":0.4,"relevance":0.4,"safety":0.4}`}, nil
}
func (scriptedJudge) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestShadowRunVerdict(t *testing.T) {
	store := NewShadowStore(10)
	metrics := map[string]int{}
	ev := NewShadowEvaluator(
		&fakeCandidate{reply: "候选回答：北京多云转晴。"},
		NewJudge(provider.NewRouter(scriptedJudge{})),
		store, 0,
	)
	ev.Metrics = func(name string) { metrics[name]++ }

	res := ev.Run(context.Background(), "alice", "sess-1", "北京天气怎么样？",
		"主回答：北京 25 度，晴朗。", "gpt-4o")

	if res.Verdict != VerdictCandidateBetter {
		t.Fatalf("候选得分更高，应为 candidate_better，实际 %s (primary=%+v candidate=%+v)",
			res.Verdict, res.PrimaryScore, res.CandidateScore)
	}
	if !strings.Contains(res.CandidateReply, "候选回答") {
		t.Fatalf("候选回答未记录: %q", res.CandidateReply)
	}
	if metrics["shadow_runs_total"] != 1 || metrics["shadow_candidate_better_total"] != 1 {
		t.Fatalf("指标未计数: %v", metrics)
	}
	// 记录落库
	recent := store.Recent("", 1)
	if len(recent) != 1 || recent[0].Verdict != VerdictCandidateBetter {
		t.Fatalf("记录未落库: %+v", recent)
	}
}

func TestShadowRunError(t *testing.T) {
	ev := NewShadowEvaluator(&fakeCandidate{err: errors.New("模型挂了")}, nil, NewShadowStore(5), 0)
	res := ev.Run(context.Background(), "bob", "s", "你好", "主回答", "m1")
	if res.Verdict != VerdictError || !strings.Contains(res.Error, "candidate") {
		t.Fatalf("候选失败应记 error，实际 %s / %q", res.Verdict, res.Error)
	}
}

func TestShadowSample(t *testing.T) {
	ev := NewShadowEvaluator(&fakeCandidate{reply: "x"}, nil, nil, 0.5)
	ev.randSrc = func() float64 { return 0.1 } // 抽中
	if !ev.WantSample(false) {
		t.Fatal("0.1 < 0.5 应抽中")
	}
	ev.randSrc = func() float64 { return 0.9 } // 未抽中
	if ev.WantSample(false) {
		t.Fatal("0.9 >= 0.5 不应抽中")
	}
	if !ev.WantSample(true) { // 显式触发必进
		t.Fatal("显式触发应忽略采样率")
	}
	if NewShadowEvaluator(&fakeCandidate{}, nil, nil, 0).WantSample(false) {
		t.Fatal("采样率 0 应关闭自动影子")
	}
}

func TestShadowStoreIsolation(t *testing.T) {
	store := NewShadowStore(3)
	store.Add(&ShadowResult{User: "alice", Verdict: "tie"})
	store.Add(&ShadowResult{User: "bob", Verdict: "tie"})
	store.Add(&ShadowResult{User: "alice", Verdict: "tie"})

	alice := store.Recent("alice", 0)
	if len(alice) != 2 {
		t.Fatalf("alice 应看到 2 条，实际 %d", len(alice))
	}
	all := store.Recent("", 2)
	if len(all) != 2 {
		t.Fatalf("应截断为 2 条，实际 %d", len(all))
	}
}
