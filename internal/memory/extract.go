// 画像事实抽取器：从"规则抽取"升级为"LLM 抽取 + 规则回退"。
//
// 背景： 的规则抽取（正则）只能识别固定句式（"我叫X"），换种说法就漏。
// LLM 语义理解更强，但贵且可能跑飞。本文件用"双保险"：
//  1. LLM 结构化抽取（StructuredChat 强约束 JSON）
//  2. 失败/空结果 → 自动回退规则抽取（零成本保底）
//
// 生产演化方向：抽取结果按置信度阈值入库；LLM 抽取可异步批量跑
// （对话时不阻塞），并配合遗忘机制做画像保鲜。
package memory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ericthz/zebra/internal/provider"
)

// Extractor 画像事实抽取器接口（规则 / LLM 可替换）。
type Extractor interface {
	Extract(ctx context.Context, text string) ([]Fact, error)
}

// RuleExtractor 规则抽取（原有能力，作为默认与回退实现）。
type RuleExtractor struct{}

// Extract 调用规则抽取器（纯正则，零成本、确定性）。
func (RuleExtractor) Extract(_ context.Context, text string) ([]Fact, error) {
	return ExtractFacts(text), nil
}

// FactsSchema LLM 抽取输出 JSON Schema（强约束 + 事后校验复用）。
var FactsSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"facts": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type": "string",
						"enum": []string{"name", "preference", "hobby", "location", "work", "age", "birthday", "other"},
					},
					"value":      map[string]interface{}{"type": "string"},
					"confidence": map[string]interface{}{"type": "number"},
				},
				"required": []string{"key", "value"},
			},
		},
	},
	"required": []string{"facts"},
}

// LLMExtractor LLM 画像抽取器：结构化输出提炼事实，失败回退规则。
type LLMExtractor struct {
	Router   *provider.Router // 抽取模型（可与生产模型相同或独立小模型）
	Timeout  time.Duration    // 单次抽取超时（默认 15s）
	Fallback Extractor        // 失败回退（nil 用 RuleExtractor）
}

// Extract 用 LLM 抽取画像事实；任何失败/空结果都回退到规则抽取。
func (l *LLMExtractor) Extract(ctx context.Context, text string) ([]Fact, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout())
	defer cancel()

	prompt := `你是用户画像抽取器。从下面的用户陈述中抽取可长期记住的事实（姓名/偏好/爱好/地点/职业/年龄/生日等），输出 JSON（不要输出其它内容）：
{"facts":[{"key":"name","value":"小明","confidence":0.9}]}
key 取值：name / preference / hobby / location / work / age / birthday / other。
只提取明确陈述的事实；不确定就留空 facts 数组。
用户陈述：
` + text

	data, err := provider.StructuredChat(ctx, l.Router,
		[]provider.Message{{Role: "user", Content: prompt}}, FactsSchema)
	if err != nil {
		return l.fallback().Extract(ctx, text) // LLM 挂了 → 规则兜底
	}
	var out struct {
		Facts []Fact `json:"facts"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return l.fallback().Extract(ctx, text) // 解析失败 → 规则兜底
	}
	// 过滤空值，保留有效事实
	valid := out.Facts[:0]
	for _, f := range out.Facts {
		if f.Key != "" && f.Value != "" {
			valid = append(valid, f)
		}
	}
	if len(valid) == 0 {
		return l.fallback().Extract(ctx, text) // LLM 没抽到 → 规则兜底
	}
	return valid, nil
}

func (l *LLMExtractor) timeout() time.Duration {
	if l.Timeout <= 0 {
		return 15 * time.Second
	}
	return l.Timeout
}

func (l *LLMExtractor) fallback() Extractor {
	if l.Fallback != nil {
		return l.Fallback
	}
	return RuleExtractor{}
}
