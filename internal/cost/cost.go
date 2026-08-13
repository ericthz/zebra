// Package cost 成本与资源治理（P5）。
//
// 背景：LLM 按 token 计费，企业必须知道"钱花哪了"。zebra 此前只有裸计数器。
// 本包实现：
//   - CostTracker：按 用户×会话 记录 in/out token，结合单价表估算成本
//   - 提供 Prometheus 文本输出（挂 /metrics/cost），支撑账单与预算告警
//
// 单价模型（简化）：每百万 token 价格，按模型名查找；未知模型给默认价。
// 生产演化方向：
//   - 精确单价从计费平台/合同读取；按账期拆分月度账单
//   - 预算告警：用量超阈值触发 notify（复用 P4）
package cost

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// price 每百万 token 的美元价格（输入/输出分开）。
// 用 map 存储示例单价；生产从配置/计费接口读取。
var defaultPrices = map[string][2]float64{ // [in, out] 每 1M tokens 美元
	"gpt-4o-mini":         {0.15, 0.60},
	"gpt-4o":              {2.50, 10.00},
	"claude-3-5-haiku":    {0.80, 4.00},
	"qwen3.5:0.8b":        {0.05, 0.10},
}

// Tracker 成本归因器（并发安全）。
type Tracker struct {
	mu      sync.Mutex
	perUser map[string]*userCost
	perSess map[string]*sessionCost
}

type userCost struct {
	inTokens  int64
	outTokens int64
}

type sessionCost struct {
	model     string
	inTokens  int64
	outTokens int64
}

// NewTracker 构造。
func NewTracker() *Tracker {
	return &Tracker{perUser: make(map[string]*userCost), perSess: make(map[string]*sessionCost)}
}

// Record 记录一次调用的 token 用量（由 Agent 的 OnUsage 回调触发）。
func (t *Tracker) Record(user, session, model string, inTokens, outTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if u, ok := t.perUser[user]; ok {
		u.inTokens += int64(inTokens)
		u.outTokens += int64(outTokens)
	} else {
		t.perUser[user] = &userCost{int64(inTokens), int64(outTokens)}
	}

	if s, ok := t.perSess[session]; ok {
		s.inTokens += int64(inTokens)
		s.outTokens += int64(outTokens)
	} else {
		t.perSess[session] = &sessionCost{model, int64(inTokens), int64(outTokens)}
	}
}

// Estimate 按单价表估算一次调用的成本（美元）。
// 返回输入/输出单价与估算总额，供展示与预警。
func Estimate(model string, inTokens, outTokens int) (costUSD float64) {
	price, ok := defaultPrices[model]
	if !ok {
		price = [2]float64{1.0, 2.0} // 未知模型给保守默认价
	}
	return price[0]*float64(inTokens)/1e6 + price[1]*float64(outTokens)/1e6
}

// Snapshot 生成 Prometheus 文本格式的成本/用量快照。
func (t *Tracker) Snapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var b strings.Builder
	// 按用户
	users := make([]string, 0, len(t.perUser))
	for u := range t.perUser {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, u := range users {
		c := t.perUser[u]
		fmt.Fprintf(&b, "zebra_cost_user_tokens_in{user=%q} %d\n", u, c.inTokens)
		fmt.Fprintf(&b, "zebra_cost_user_tokens_out{user=%q} %d\n", u, c.outTokens)
	}
	// 按会话
	sess := make([]string, 0, len(t.perSess))
	for s := range t.perSess {
		sess = append(sess, s)
	}
	sort.Strings(sess)
	for _, s := range sess {
		c := t.perSess[s]
		fmt.Fprintf(&b, "zebra_cost_session_tokens_in{session=%q,model=%q} %d\n", s, c.model, c.inTokens)
		fmt.Fprintf(&b, "zebra_cost_session_tokens_out{session=%q,model=%q} %d\n", s, c.model, c.outTokens)
	}
	return b.String()
}

// Handler HTTP 处理器：挂到 /metrics/cost。
func (t *Tracker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, t.Snapshot())
	}
}
