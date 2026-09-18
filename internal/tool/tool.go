// Package tool 内置工具系统：注册、参数校验、权限边界。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Tool 工具接口。Execute 必须带上 ctx（支持超时/取消）。
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{} // JSON Schema 子集
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// Risky 可选接口：工具声明自己的风险等级与可用角色（工具安全边界）。
// 未实现该接口的工具视为 0 级（安全、所有角色可用）。
type Risky interface {
	RiskLevel() int         // 0 安全 / 1 中风险（记录审计）/ 2 高危（需二次确认）
	AllowedRoles() []string // 允许的角色；空 slice 表示所有角色
}

// Result 工具执行结果（统一返回给模型的结构化格式）。
type Result struct {
	OK      bool   `json:"ok"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// ToResult 统一包装工具输出，便于解析与审计。
func ToResult(content string) string {
	b, _ := json.Marshal(Result{OK: true, Content: content})
	return string(b)
}

// ParseArguments 解析模型返回的工具参数（兼容对象或 JSON 字符串两种形态）。
func ParseArguments(raw json.RawMessage) (map[string]interface{}, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(raw, &args); err == nil {
		return args, nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err != nil {
		return nil, fmt.Errorf("无法解析 arguments: %v", err)
	}
	if err := json.Unmarshal([]byte(str), &args); err != nil {
		return nil, fmt.Errorf("arguments 字符串无效: %v", err)
	}
	return args, nil
}

// ValidateArgs 按工具 schema 校验参数（结构化输出）：
// required 字段必须存在，且类型匹配（string/number/integer/boolean）。
func ValidateArgs(t Tool, args map[string]interface{}) error {
	schema := t.Parameters()
	props, _ := schema["properties"].(map[string]interface{})

	if required := requiredArgs(schema); len(required) > 0 {
		for _, field := range required {
			if _, exists := args[field]; !exists {
				return fmt.Errorf("缺少必填参数: %s", field)
			}
		}
	}

	for name, value := range args {
		def, ok := props[name].(map[string]interface{})
		if !ok {
			continue
		}
		want, _ := def["type"].(string)
		if want == "" || value == nil {
			continue
		}
		if !typeMatches(value, want) {
			return fmt.Errorf("参数 %s 类型错误: 期望 %s，实际 %s", name, want, reflect.TypeOf(value).Kind())
		}
	}
	return nil
}

// requiredArgs 归一化 schema.required，兼容 []string 与 []interface{} 两种声明形态
// （与 internal/schema 保持一致，避免 []string 声明的必填参数被静默跳过）。
func requiredArgs(schema map[string]interface{}) []string {
	if req, ok := schema["required"].([]string); ok {
		return req
	}
	if req, ok := schema["required"].([]interface{}); ok {
		names := make([]string, 0, len(req))
		for _, n := range req {
			names = append(names, fmt.Sprint(n))
		}
		return names
	}
	return nil
}

func typeMatches(v interface{}, want string) bool {
	switch want {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		switch n := v.(type) {
		case int, int32, int64:
			return true
		case float64:
			return n == float64(int64(n))
		case float32:
			return n == float32(int64(n))
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]interface{})
		return ok
	case "object":
		_, ok := v.(map[string]interface{})
		return ok
	}
	return true
}

// StringArg 安全读取字符串参数。
func StringArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

// StringSliceArg 读取字符串切片参数（模型 JSON 解码后是 []interface{}）。
func StringSliceArg(args map[string]interface{}, key string) []string {
	raw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// FloatSliceArg 读取浮点切片参数（JSON number 统一解为 float64）。
func FloatSliceArg(args map[string]interface{}, key string) []float64 {
	raw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(raw))
	for _, v := range raw {
		switch n := v.(type) {
		case float64:
			out = append(out, n)
		case int:
			out = append(out, float64(n))
		case int64:
			out = append(out, float64(n))
		}
	}
	return out
}

// FloatArg 安全读取数值参数。
func FloatArg(args map[string]interface{}, key string) (float64, bool) {
	f, ok := args[key].(float64)
	return f, ok
}
