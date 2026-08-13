package eval

import (
	"testing"
	"time"
)

func TestShadowStats(t *testing.T) {
	store := NewShadowStore(100)
	seed := []string{
		VerdictCandidateBetter, VerdictCandidateBetter, VerdictCandidateBetter,
		VerdictPrimaryBetter, VerdictTie, VerdictError,
	}
	for _, v := range seed {
		store.Add(&ShadowResult{Time: time.Now(), User: "alice", Verdict: v})
	}
	store.Add(&ShadowResult{Time: time.Now(), User: "bob", Verdict: VerdictCandidateBetter})

	all := store.Stats("")
	if all.Total != 7 || all.CandidateBetter != 4 || all.PrimaryBetter != 1 || all.Tie != 1 || all.Errors != 1 {
		t.Fatalf("聚合统计错误: %+v", all)
	}
	// 有效样本 6 条，候选胜 4 → win_rate = 4/6
	if all.WinRate < 0.66 || all.WinRate > 0.67 {
		t.Fatalf("胜率计算错误: %f", all.WinRate)
	}

	// 用户维度过滤（A4）
	alice := store.Stats("alice")
	if alice.Total != 6 || alice.CandidateBetter != 3 {
		t.Fatalf("用户过滤错误: %+v", alice)
	}
}

func TestRecommendSwitch(t *testing.T) {
	// 样本不足 → 观望
	if r := RecommendSwitch(ShadowStats{Total: 3, CandidateBetter: 3}, 10, 60); r.Action != ActionInsufficient {
		t.Fatalf("样本不足应观望: %+v", r)
	}
	// 样本达标 + 胜率达标 → 建议切换
	rec := RecommendSwitch(ShadowStats{Total: 10, CandidateBetter: 7, Errors: 1}, 10, 60)
	if rec.Action != ActionPromote {
		t.Fatalf("胜率 77%% 应建议切换: %+v", rec)
	}
	// 胜率不足 → 保持主模型
	if r := RecommendSwitch(ShadowStats{Total: 10, CandidateBetter: 3}, 10, 60); r.Action != ActionKeepPrimary {
		t.Fatalf("胜率 30%% 应保持主模型: %+v", r)
	}
	// 有效样本为 0 → 保持
	if r := RecommendSwitch(ShadowStats{Total: 10, Errors: 10}, 10, 60); r.Action != ActionKeepPrimary {
		t.Fatalf("全失败应保持主模型: %+v", r)
	}
}
