// 查询改写：把用户问题改写得更清晰、更适合检索与回答。
//
// 背景：用户口语问题常有指代、省略（"那它多少钱？"），直接检索效果差。
// 改写（补全指代、明确主题）后，RAG/技能检索与最终回答都更精准。
// 打开方式：Agent.Config.RewriteQuery=true（server 由 ZEBRA_QUERY_REWRITE=1 开启）。
// 可靠性：改写失败时原样返回原问题，不阻断。
package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ericthz/zebra/internal/provider"
)

// rewriteSchema 改写输出 JSON Schema。
var rewriteSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"rewritten": map[string]interface{}{"type": "string"},
	},
	"required": []string{"rewritten"},
}

// RewriteQuery 改写用户问题；失败时返回原问题（不阻断）。
func (a *Agent) RewriteQuery(ctx context.Context, question string) (string, error) {
	prompt := "请把下面的用户问题改写得更清晰、更适合知识库检索（补全指代、明确主题与约束），只输出 JSON：\n" +
		"{\"rewritten\":\"改写后的问题\"}\n原问题：" + question
	data, err := provider.StructuredChat(ctx, a.cfg.Router,
		[]provider.Message{{Role: "user", Content: prompt}}, rewriteSchema)
	if err != nil {
		return question, nil
	}
	var out struct {
		Rewritten string `json:"rewritten"`
	}
	if err := json.Unmarshal(data, &out); err != nil || strings.TrimSpace(out.Rewritten) == "" {
		return question, nil
	}
	return out.Rewritten, nil
}

// rewriteForRetrieval 供 buildMessages 使用：改写成功才替换输入。
func (a *Agent) rewriteForRetrieval(ctx context.Context, input string) string {
	rewritten, err := a.RewriteQuery(ctx, input)
	if err != nil || rewritten == "" {
		return input
	}
	return rewritten
}
