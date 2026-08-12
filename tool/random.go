// tool/random.go
package tool

import (
	"fmt"
	"math/rand"
	"time"
)

type RandomTool struct{}

func (r *RandomTool) Name() string { return "generate_random_number" }

func (r *RandomTool) Description() string {
	return "生成指定范围内的随机整数（包含 min 和 max）。"
}

func (r *RandomTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"min": map[string]interface{}{
				"type":        "integer",
				"description": "最小值",
			},
			"max": map[string]interface{}{
				"type":        "integer",
				"description": "最大值",
			},
		},
		"required": []string{"min", "max"},
	}
}

func (r *RandomTool) Execute(args map[string]interface{}) (string, error) {
	minF, ok := args["min"].(float64)
	if !ok {
		return "", fmt.Errorf("min 参数错误")
	}
	maxF, ok := args["max"].(float64)
	if !ok {
		return "", fmt.Errorf("max 参数错误")
	}
	min := int(minF)
	max := int(maxF)
	if min > max {
		return "", fmt.Errorf("min 不能大于 max")
	}
	rand.Seed(time.Now().UnixNano())
	result := rand.Intn(max-min+1) + min
	return fmt.Sprintf("%d", result), nil
}
