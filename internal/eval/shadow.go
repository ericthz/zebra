// 在线评测 / 影子模式：上线新模型前，先把真实流量"复制"一份给候选模型。
//
// 背景：直接换模型有风险（质量回退不可见）。影子模式让候选模型和主模型
// 同时回答同一个问题，主模型照常服务用户（用户无感知），后台用 Judge 给
// 两份回答分别打分、比较胜负，形成"候选模型 vs 主模型"的回归数据。
// 积累足够样本后，再决定是否切换/灰度。
//
// 流程：
//
//	用户请求 → 主模型回答（返回给用户）
//	           └→ 候选模型独立回答同一问题（不喂主模型答案，保证独立）
//	              └→ Judge 双评（主/候选）→ 对比总分 → 落影子记录 + 指标
//
// 生产演化方向：
//   - 采样率按路由/租户/模型分层；结果落库进评测看板
//   - 候选模型回答失败不算"更差"，单独计 error 分类
package eval

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/ericthz/zebra/internal/provider"
)

// Verdict 影子对比结论。
const (
	VerdictCandidateBetter = "candidate_better" // 候选模型明显更好
	VerdictPrimaryBetter   = "primary_better"   // 主模型明显更好
	VerdictTie             = "tie"              // 差距在容差内
	VerdictError           = "error"            // 候选或评审失败
)

// ShadowResult 一次影子评测记录。
type ShadowResult struct {
	ID             string    `json:"id"`
	Time           time.Time `json:"time"`
	User           string    `json:"user"`
	Session        string    `json:"session,omitempty"`
	Question       string    `json:"question"`
	PrimaryReply   string    `json:"primary_reply"`
	CandidateReply string    `json:"candidate_reply,omitempty"`
	PrimaryModel   string    `json:"primary_model"`
	CandidateModel string    `json:"candidate_model"`
	PrimaryScore   *Scores   `json:"primary_score,omitempty"`
	CandidateScore *Scores   `json:"candidate_score,omitempty"`
	Verdict        string    `json:"verdict"`
	Error          string    `json:"error,omitempty"`
}

// ShadowStore 影子记录内存存储（并发安全，生产换 DB/时序库）。
type ShadowStore struct {
	mu   sync.Mutex
	max  int
	runs []*ShadowResult
}

// NewShadowStore 构造，max 为保留上限（超出丢最旧）。
func NewShadowStore(max int) *ShadowStore {
	if max <= 0 {
		max = 200
	}
	return &ShadowStore{max: max}
}

// Add 追加一条记录并分配 ID，返回同一指针。
func (s *ShadowStore) Add(r *ShadowResult) *ShadowResult {
	r.ID = "shadow-" + itoaTime(r.Time.UnixNano())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
	if len(s.runs) > s.max {
		s.runs = s.runs[len(s.runs)-s.max:]
	}
	return r
}

// Recent 返回最近的 n 条（新在前）；user 非空则只返回该用户（隔离）。
func (s *ShadowStore) Recent(user string, n int) []*ShadowResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*ShadowResult, 0, len(s.runs))
	for i := len(s.runs) - 1; i >= 0; i-- {
		r := s.runs[i]
		if user != "" && r.User != user {
			continue
		}
		out = append(out, r)
		if n > 0 && len(out) >= n {
			break
		}
	}
	return out
}

// ShadowEvaluator 影子评测执行器。
type ShadowEvaluator struct {
	mu         sync.RWMutex
	Candidate  provider.Provider // 候选模型（与主模型解耦，独立回答；promote/回滚时重指向）
	Judge      *Judge            // 评审器（给两份回答打分）
	Store      *ShadowStore      // 记录落点（nil 则只打指标不落库）
	SampleRate float64           // 自动采样率 0~1；0 表示仅显式触发
	Metrics    func(name string) // 指标回调（nil 忽略）
	Log        *slog.Logger
	randSrc    func() float64 // 采样随机源（测试可注入固定值）
}

// NewShadowEvaluator 构造（sampleRate 0~1；Judge 为 nil 时退化为只对比文本相似度）。
func NewShadowEvaluator(candidate provider.Provider, judge *Judge, store *ShadowStore, sampleRate float64) *ShadowEvaluator {
	return &ShadowEvaluator{Candidate: candidate, Judge: judge, Store: store, SampleRate: sampleRate}
}

// SetCandidate 原子重指向候选模型。promote 后必须调用，否则候选仍是
// 已提升为新主的模型 → 影子变成"新主 vs 自己"的自我对比，胜率数据被污染。
func (e *ShadowEvaluator) SetCandidate(c provider.Provider) {
	e.mu.Lock()
	e.Candidate = c
	e.mu.Unlock()
}

// CandidateName 线程安全地读取候选模型名（看板/日志用）。
func (e *ShadowEvaluator) CandidateName() string {
	return e.candidateName()
}

// candidateName 锁内读候选名。
func (e *ShadowEvaluator) candidateName() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.Candidate == nil {
		return ""
	}
	return e.Candidate.Name()
}

// candidateChat 锁内读候选并调用（保持重指向与读取原子一致）。
func (e *ShadowEvaluator) candidateChat(ctx context.Context, msgs []provider.Message, tools []provider.Tool) (provider.Message, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.Candidate == nil {
		return provider.Message{}, errors.New("candidate 未配置")
	}
	return e.Candidate.Chat(ctx, msgs, tools)
}

// WantSample 决定这次请求是否进影子：显式触发必进；否则按采样率抽签。
func (e *ShadowEvaluator) WantSample(explicit bool) bool {
	if explicit {
		return true
	}
	if e.SampleRate <= 0 {
		return false
	}
	if e.randSrc != nil {
		return e.randSrc() < e.SampleRate
	}
	return rand.Float64() < e.SampleRate
}

// Run 执行一次影子评测（同步，调用方决定放 goroutine 还是阻塞）。
// primaryReply 是主模型已返回给用户的回答；候选模型用原始问题独立回答。
func (e *ShadowEvaluator) Run(ctx context.Context, user, session, question, primaryReply, primaryModel string) *ShadowResult {
	res := &ShadowResult{
		Time: time.Now(), User: user, Session: session,
		Question: question, PrimaryReply: primaryReply,
		PrimaryModel: primaryModel, CandidateModel: e.candidateName(),
	}
	e.inc("shadow_runs_total")

	// 1. 候选模型独立回答（不喂主模型答案，防止"抄袭"造成虚假一致）
	cand, err := e.candidateChat(ctx, []provider.Message{{Role: "user", Content: question}}, nil)
	if err != nil {
		res.Verdict = VerdictError
		res.Error = "candidate: " + err.Error()
		e.inc("shadow_errors_total")
		e.finish(res)
		return res
	}
	res.CandidateReply = cand.Content

	// 2. Judge 双评：同一个评审标准分别打主/候选两份回答
	ps, perr := e.score(ctx, question, primaryReply)
	cs, cerr := e.score(ctx, question, cand.Content)
	switch {
	case perr != nil || cerr != nil:
		res.Verdict = VerdictError
		res.Error = "judge: " + firstErr(perr, cerr)
		e.inc("shadow_errors_total")
	case ps == nil || cs == nil:
		res.Verdict = VerdictError
		res.Error = "judge: empty score"
		e.inc("shadow_errors_total")
	default:
		res.PrimaryScore, res.CandidateScore = ps, cs
		res.Verdict = compare(ps, cs)
		switch res.Verdict {
		case VerdictCandidateBetter:
			e.inc("shadow_candidate_better_total")
		case VerdictPrimaryBetter:
			e.inc("shadow_primary_better_total")
		default:
			e.inc("shadow_tie_total")
		}
	}
	e.finish(res)
	return res
}

func (e *ShadowEvaluator) score(ctx context.Context, question, answer string) (*Scores, error) {
	if e.Judge == nil {
		// 无评审模型时降级：用忠实度代理 = 与主回答的字符重叠率（粗粒度）
		return &Scores{Faithfulness: overlapRatio(answer, question), Relevance: 1}, nil
	}
	return e.Judge.Score(ctx, question, answer)
}

func (e *ShadowEvaluator) finish(res *ShadowResult) {
	if e.Log != nil {
		e.Log.Info("shadow run", "id", res.ID, "verdict", res.Verdict,
			"primary", res.PrimaryModel, "candidate", res.CandidateModel)
	}
	if e.Store != nil {
		e.Store.Add(res)
	}
}

func (e *ShadowEvaluator) inc(name string) {
	if e.Metrics != nil {
		e.Metrics(name)
	}
}

// compare 按三维总分比较，容差 0.1（评委浮点抖动不误判胜负）。
func compare(p, c *Scores) string {
	dp := p.Faithfulness + p.Relevance + p.Safety
	dc := c.Faithfulness + c.Relevance + c.Safety
	switch {
	case dc-dp > 0.1:
		return VerdictCandidateBetter
	case dp-dc > 0.1:
		return VerdictPrimaryBetter
	default:
		return VerdictTie
	}
}

func firstErr(a, b error) string {
	if a != nil {
		return a.Error()
	}
	return b.Error()
}

// overlapRatio 无 Judge 时的降级相似度：回答含问题关键词的比例（0~1）。
func overlapRatio(answer, question string) float64 {
	if answer == "" || question == "" {
		return 0
	}
	seen := map[rune]bool{}
	hit := 0
	for _, r := range question {
		if seen[r] {
			continue
		}
		seen[r] = true
		if containsRune(answer, r) {
			hit++
		}
	}
	return float64(hit) / float64(len(seen))
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// itoaTime 简单整数转字符串（ID 用，避免引入 strconv 也够用）。
func itoaTime(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [32]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
