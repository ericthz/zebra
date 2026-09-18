// Package schema 结构化输出强约束。
//
// 背景：zebra 之前对"结构化输出"是【事后校验】（工具参数解析失败再反馈重试）。
// 成熟产品更进一步：在【生成前】约束（constrained decoding / response_format），
// 在【生成后】用 schema 严格校验 —— 双保险。本包实现后者（通用 JSON Schema 子集
// 校验器），配合 provider 层 response_format（见 internal/provider/openai.go）。
//
// 支持的子集（够用且好懂）：
//
//	type: object/string/number/integer/boolean/array
//	required / properties / enum / items（数组元素 schema）
//
// 生产演化方向：换官方 json-schema 库（santhosh-tekuri/jsonschema）支持完整规范；
// 校验结果与重试策略绑定（校验不过 → 反馈给模型重新生成）。
package schema

import (
	"encoding/json"
	"fmt"
)

// Validate 校验 data 是否满足 schema；不满足返回首个错误。
func Validate(data []byte, sch map[string]interface{}) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("不是合法 JSON: %w", err)
	}
	return check(v, sch, "$")
}

// check 递归校验一个值。
func check(v interface{}, sch map[string]interface{}, path string) error {
	typ, _ := sch["type"].(string)

	// enum 约束（兼容 []string 与 []interface{} 两种声明形态，见 enumValues）
	if enum := enumValues(sch); len(enum) > 0 {
		for _, e := range enum {
			if jsonEqual(v, e) {
				return nil // 命中 enum 即通过（不再检查类型，enums 已隐含）
			}
		}
		return fmt.Errorf("%s: 值 %v 不在允许列表 %v", path, v, enum)
	}

	switch typ {
	case "object":
		obj, ok := v.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: 期望 object，实际 %T", path, v)
		}
		// required
		for _, name := range requiredNames(sch) {
			if _, exists := obj[name]; !exists {
				return fmt.Errorf("%s: 缺少必填字段 %s", path, name)
			}
		}
		// properties
		if props, ok := sch["properties"].(map[string]interface{}); ok {
			for name, ps := range props {
				if pv, exists := obj[name]; exists {
					if psch, ok := ps.(map[string]interface{}); ok {
						if err := check(pv, psch, path+"."+name); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	case "array":
		arr, ok := v.([]interface{})
		if !ok {
			return fmt.Errorf("%s: 期望 array，实际 %T", path, v)
		}
		if items, ok := sch["items"].(map[string]interface{}); ok {
			for i, item := range arr {
				if err := check(item, items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
		return nil
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s: 期望 string，实际 %T", path, v)
		}
		return nil
	case "integer":
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return fmt.Errorf("%s: 期望 integer，实际 %v", path, v)
		}
		return nil
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("%s: 期望 number，实际 %T", path, v)
		}
		return nil
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: 期望 boolean，实际 %T", path, v)
		}
		return nil
	default:
		return nil // 未声明 type：跳过（宽松模式）
	}
}

// jsonEqual 深度比较两个值（用于 enum）。
func jsonEqual(a, b interface{}) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// enumValues 归一化 enum 声明，兼容 []string 与 []interface{} 两种形态。
// 历史上 schema 混用两种类型；只按 []interface{} 断言会导致 []string 声明的
// enum 静默失效（如 debate.go 的 winner、memory/extract.go 的类型枚举）。
// 返回 []interface{} 以便 jsonEqual 深度比较。
func enumValues(sch map[string]interface{}) []interface{} {
	if enum, ok := sch["enum"].([]string); ok {
		out := make([]interface{}, 0, len(enum))
		for _, e := range enum {
			out = append(out, e)
		}
		return out
	}
	if enum, ok := sch["enum"].([]interface{}); ok {
		return enum
	}
	return nil
}

// requiredNames 归一化 required 字段，兼容 []string 与 []interface{} 两种声明形态。
// 历史上各处 schema 混用两种类型，若只按 []interface{} 断言会导致 []string 声明的
// 必填字段被静默跳过（不校验）。
func requiredNames(sch map[string]interface{}) []string {
	if req, ok := sch["required"].([]string); ok {
		return req
	}
	if req, ok := sch["required"].([]interface{}); ok {
		names := make([]string, 0, len(req))
		for _, n := range req {
			names = append(names, fmt.Sprint(n))
		}
		return names
	}
	return nil
}
