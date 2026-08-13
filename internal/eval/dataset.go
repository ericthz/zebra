// 评测数据集管理（P49）：把评测从"写在测试里"升级为"目录化管理 + 回归对比"。
//
// 背景：golden_test.go 的用例写死在代码里，扩充/换模型后无法对比"这轮比
// 上轮好还是差"。本文件提供：
//   - Case：一条评测用例（ID/问题/期望/标签）
//   - LoadCases：从目录加载 *.json 用例集（便于团队协作与版本管理）
//   - RunCases：批量执行（模型回答 + Judge 打分）
//   - BaselineDiff：两轮结果按 ID 对比，输出 改进/回退/持平 报告
//
// 生产演化方向：用例按任务分桶、Git 化版本、CI 跑分并自动生成 diff。
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericthz/zebra/internal/provider"
)

// Case 一条评测用例。
type Case struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Expect   string   `json:"expect,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// CaseResult 一条用例的执行结果。
type CaseResult struct {
	Case  Case
	Reply string
	Score *Scores
	Err   string
}

// LoadCases 从 dir 读取 *.json 用例文件（每个文件 {"cases":[...]}）。
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Case
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var file struct {
			Cases []Case `json:"cases"`
		}
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, file.Cases...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no cases in %s", dir)
	}
	return out, nil
}

// RunCases 批量执行：router 回答 + Judge 打分（judge 为 nil 则只取回答）。
func RunCases(ctx context.Context, router *provider.Router, judge *Judge, cases []Case) ([]CaseResult, error) {
	out := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		res := CaseResult{Case: c}
		msg, _, err := router.ChatWithFallback(ctx, []provider.Message{{Role: "user", Content: c.Question}}, nil)
		if err != nil {
			res.Err = err.Error()
			out = append(out, res)
			continue
		}
		res.Reply = msg.Content
		if judge != nil {
			if s, serr := judge.Score(ctx, c.Question, msg.Content); serr == nil {
				res.Score = s
			} else {
				res.Err = serr.Error()
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// DiffItem 单条用例的分数变化。
type DiffItem struct {
	ID       string
	OldTotal float64
	NewTotal float64
	Delta    float64
	NewErr   string
}

// DiffReport 两轮评测的回归对比报告。
type DiffReport struct {
	Total     int
	Improved  int
	Regressed int
	Unchanged int
	Failed    int
	Items     []DiffItem
}

// BaselineDiff 对比两轮结果（按用例 ID 匹配；总分 = 三维打分之和）。
func BaselineDiff(old, cur []CaseResult) DiffReport {
	prevByID := make(map[string]CaseResult, len(old))
	for _, r := range old {
		prevByID[r.Case.ID] = r
	}
	var rep DiffReport
	for _, r := range cur {
		rep.Total++
		if r.Err != "" || r.Score == nil {
			rep.Failed++
			rep.Items = append(rep.Items, DiffItem{ID: r.Case.ID, NewErr: r.Err})
			continue
		}
		nt := r.Score.Faithfulness + r.Score.Relevance + r.Score.Safety
		item := DiffItem{ID: r.Case.ID, NewTotal: nt}
		if prev, ok := prevByID[r.Case.ID]; ok && prev.Score != nil {
			ot := prev.Score.Faithfulness + prev.Score.Relevance + prev.Score.Safety
			item.OldTotal, item.Delta = ot, nt-ot
			switch {
			case item.Delta > 0.05:
				rep.Improved++
			case item.Delta < -0.05:
				rep.Regressed++
			default:
				rep.Unchanged++
			}
		}
		rep.Items = append(rep.Items, item)
	}
	return rep
}
