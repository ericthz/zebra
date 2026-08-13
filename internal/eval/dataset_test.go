package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ericthz/zebra/internal/provider"
)

// datasetRouter 固定回答的模型。
type datasetRouter struct{ reply string }

func (d *datasetRouter) Name() string { return "dataset-router" }
func (d *datasetRouter) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: d.reply}, nil
}
func (d *datasetRouter) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

// datasetJudge 固定打分的评审模型。
type datasetJudge struct{ reply string }

func (d *datasetJudge) Name() string { return "dataset-judge" }
func (d *datasetJudge) Chat(_ context.Context, _ []provider.Message, _ []provider.Tool) (provider.Message, error) {
	return provider.Message{Content: d.reply}, nil
}
func (d *datasetJudge) ChatStream(context.Context, []provider.Message, []provider.Tool) (<-chan provider.StreamEvent, error) {
	return nil, context.Canceled
}

func TestLoadCases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"),
		[]byte(`{"cases":[{"id":"c1","question":"q1","tags":["t"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, err := LoadCases(dir)
	if err != nil || len(cases) != 1 || cases[0].ID != "c1" || cases[0].Question != "q1" {
		t.Fatalf("LoadCases 异常: %v %v", cases, err)
	}
	if _, err := LoadCases(t.TempDir()); err == nil {
		t.Fatal("空目录应报错")
	}
}

func TestRunCases(t *testing.T) {
	router := provider.NewRouter(&datasetRouter{reply: "答案"})
	judge := NewJudge(provider.NewRouter(&datasetJudge{
		reply: `{"faithfulness":0.8,"relevance":0.7,"safety":0.9}`,
	}))
	cases := []Case{{ID: "c1", Question: "q1"}, {ID: "c2", Question: "q2"}}
	res, err := RunCases(context.Background(), router, judge, cases)
	if err != nil || len(res) != 2 {
		t.Fatalf("RunCases 异常: %v %v", res, err)
	}
	if res[0].Reply != "答案" || res[0].Score.Faithfulness != 0.8 {
		t.Fatalf("回答/打分异常: %+v", res[0])
	}
}

func TestBaselineDiff(t *testing.T) {
	score := func(f, r, s float64) *Scores { return &Scores{Faithfulness: f, Relevance: r, Safety: s} }
	old := []CaseResult{
		{Case: Case{ID: "a"}, Score: score(0.8, 0.8, 0.8)},
		{Case: Case{ID: "b"}, Score: score(0.9, 0.9, 0.9)},
		{Case: Case{ID: "c"}, Score: score(0.5, 0.5, 0.5)},
	}
	cur := []CaseResult{
		{Case: Case{ID: "a"}, Score: score(0.9, 0.9, 0.9)}, // 改进 +0.3
		{Case: Case{ID: "b"}, Score: score(0.5, 0.5, 0.5)}, // 回退 -1.2
		{Case: Case{ID: "c"}, Score: score(0.5, 0.5, 0.5)}, // 持平
		{Case: Case{ID: "d"}, Err: "fail"},                 // 失败
	}
	rep := BaselineDiff(old, cur)
	if rep.Total != 4 || rep.Improved != 1 || rep.Regressed != 1 || rep.Unchanged != 1 || rep.Failed != 1 {
		t.Fatalf("回归报告异常: %+v", rep)
	}
}

// TestAppendCaseAndCaseFromFeedback P55 反馈回流：负面反馈转用例并追加数据集。
func TestAppendCaseAndCaseFromFeedback(t *testing.T) {
	dir := t.TempDir()
	c1 := CaseFromFeedback("北京天气", "晴", "答非所问")
	if c1.ID == "" || len(c1.Tags) != 2 || c1.Expect != "人工反馈：答非所问" {
		t.Fatalf("CaseFromFeedback 异常: %+v", c1)
	}
	if err := AppendCase(dir, c1); err != nil {
		t.Fatal(err)
	}
	if err := AppendCase(dir, Case{ID: "x", Question: "q"}); err != nil {
		t.Fatal(err)
	}
	cases, err := LoadCases(dir)
	if err != nil || len(cases) != 2 {
		t.Fatalf("回流用例应可被加载: %v %v", cases, err)
	}
	// 空注释的默认占位
	if c2 := CaseFromFeedback("q", "a", ""); c2.Expect != "（人工踩，待核查）" {
		t.Fatalf("空注释默认异常: %q", c2.Expect)
	}
}

func TestCountSafetyFails(t *testing.T) {
	score := func(s float64) *Scores { return &Scores{Safety: s} }
	res := []CaseResult{
		{Case: Case{ID: "a"}, Score: score(0.9)},
		{Case: Case{ID: "b"}, Score: score(0.3)},
		{Case: Case{ID: "c"}, Err: "fail"},
	}
	passed, failed, fails := CountSafetyFails(res, 0.7)
	if passed != 1 || failed != 2 || len(fails) != 2 {
		t.Fatalf("安全统计异常: passed=%d failed=%d fails=%d", passed, failed, len(fails))
	}
}
