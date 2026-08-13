// 影子评测看板与灰度切换建议（P26）。
//
// 背景：影子模式（P21）在积累"候选 vs 主模型"的对比数据，但数据躺着
// 不看等于白测。本文件把记录聚合为统计看板，并用简单策略给出灰度建议：
//
//	样本数达标（>= minSamples）且候选胜率达标（>= minWinRate%）→ 建议切换
//
// 生产演化方向：按业务线/地域/模型分桶统计；胜率与延迟、成本一起综合
// 打分；切换走金丝雀 + 自动回滚（这里保留"管理员确认后 promote"最小闭环）。
package eval

import "fmt"

// ShadowStats 影子评测聚合统计。
type ShadowStats struct {
	Total           int     `json:"total"`            // 总记录数
	CandidateBetter int     `json:"candidate_better"` // 候选更好
	PrimaryBetter   int     `json:"primary_better"`   // 主模型更好
	Tie             int     `json:"tie"`              // 平局
	Errors          int     `json:"errors"`           // 候选/评审失败
	WinRate         float64 `json:"win_rate"`         // 候选胜率 = candidate_better / 有效样本
}

// Stats 聚合当前影子记录（user 非空则只看该用户）。
func (s *ShadowStore) Stats(user string) ShadowStats {
	var st ShadowStats
	for _, r := range s.Recent(user, 0) {
		st.Total++
		switch r.Verdict {
		case VerdictCandidateBetter:
			st.CandidateBetter++
		case VerdictPrimaryBetter:
			st.PrimaryBetter++
		case VerdictTie:
			st.Tie++
		case VerdictError:
			st.Errors++
		}
	}
	judged := st.Total - st.Errors
	if judged > 0 {
		st.WinRate = float64(st.CandidateBetter) / float64(judged)
	}
	return st
}

// SwitchAction 切换建议动作。
const (
	ActionPromote      = "promote_candidate" // 建议把候选提升为主模型
	ActionKeepPrimary  = "keep_primary"      // 建议继续用主模型
	ActionInsufficient = "insufficient_data" // 样本不足，先观望
)

// SwitchRecommendation 灰度切换建议。
type SwitchRecommendation struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// RecommendSwitch 基于统计给出切换建议。
// minSamples 最小样本数；minWinRate 候选胜率阈值（百分比）。
func RecommendSwitch(st ShadowStats, minSamples, minWinRate int) SwitchRecommendation {
	if st.Total < minSamples {
		return SwitchRecommendation{Action: ActionInsufficient,
			Reason: fmt.Sprintf("样本不足：已收集 %d/%d 条", st.Total, minSamples)}
	}
	judged := st.Total - st.Errors
	if judged == 0 {
		return SwitchRecommendation{Action: ActionKeepPrimary, Reason: "有效样本为 0（候选/评审全部失败）"}
	}
	winRate := float64(st.CandidateBetter) / float64(judged) * 100
	if winRate >= float64(minWinRate) {
		return SwitchRecommendation{Action: ActionPromote,
			Reason: fmt.Sprintf("候选胜率 %.1f%% ≥ %d%%（样本 %d 条）", winRate, minWinRate, judged)}
	}
	return SwitchRecommendation{Action: ActionKeepPrimary,
		Reason: fmt.Sprintf("候选胜率 %.1f%% < %d%%（样本 %d 条）", winRate, minWinRate, judged)}
}
