package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// TestAppendCaseCorruptBackup P0-5：feedback.json 损坏（半截写入）时，
// 不得静默当空数据重写——那样会"清空"既有数据集。必须先把损坏文件备份
// 为 .bak 保留现场，再追加。
func TestAppendCaseCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feedback.json")

	// 先写一条合法用例（既有数据集）
	if err := AppendCase(dir, Case{ID: "keep", Question: "q1"}); err != nil {
		t.Fatal(err)
	}
	// 模拟崩溃留下的半截文件：损坏但不删除
	if err := os.WriteFile(path, []byte(`{"cases":[{"id":"part`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AppendCase(dir, Case{ID: "new", Question: "q2"}); err != nil {
		t.Fatalf("损坏后追加应成功（备份后重建）: %v", err)
	}
	// 损坏现场应被保留为 .bak，不得被静默覆盖丢弃
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("损坏文件应备份为 .bak: %v", err)
	}
	cases, err := LoadCases(dir)
	if err != nil || len(cases) != 1 || cases[0].ID != "new" {
		t.Fatalf("追加后的数据集应为 1 条新用例（半截数据不混入）: %v %v", cases, err)
	}
}

// TestAppendCaseAtomic P0-5：写入走临时文件+rename 原子落盘，
// 不会留下半截文件（这是 P0-5 的另一半：损坏文件的来源）。
func TestAppendCaseAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feedback.json")
	if err := AppendCase(dir, Case{ID: "a", Question: "q"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []Case `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil || len(file.Cases) != 1 {
		t.Fatalf("落盘内容应可解析且含 1 条用例: %v %v", file, err)
	}
	// 目录中不应残留临时文件
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("不应残留临时文件: %s", e.Name())
		}
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
