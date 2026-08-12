// tool/unit_converter.go
package tool

import (
	"fmt"
	"strings"
)

type UnitConverterTool struct{}

func (u *UnitConverterTool) Name() string { return "convert_units" }

func (u *UnitConverterTool) Description() string {
	return "在不同单位之间进行转换，支持长度、重量、温度等。"
}

func (u *UnitConverterTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value": map[string]interface{}{
				"type":        "number",
				"description": "待转换的数值",
			},
			"from_unit": map[string]interface{}{
				"type":        "string",
				"description": "源单位，例如：km, mi, kg, lb, C, F",
			},
			"to_unit": map[string]interface{}{
				"type":        "string",
				"description": "目标单位，例如：m, ft, g, oz, K",
			},
		},
		"required": []string{"value", "from_unit", "to_unit"},
	}
}

var conversions = map[string]map[string]float64{
	"km": {"m": 1000, "mi": 0.621371, "ft": 3280.84},
	"m":  {"km": 0.001, "cm": 100, "ft": 3.28084},
	"mi": {"km": 1.60934, "m": 1609.34, "ft": 5280},
	"kg": {"g": 1000, "lb": 2.20462, "oz": 35.274},
	"g":  {"kg": 0.001, "oz": 0.035274},
	"lb": {"kg": 0.453592, "g": 453.592, "oz": 16},
	"C":  {}, // 温度特殊处理
	"F":  {},
	"K":  {},
}

func convertTemp(value float64, from, to string) (float64, error) {
	// 先转成摄氏度
	var celsius float64
	switch strings.ToUpper(from) {
	case "C":
		celsius = value
	case "F":
		celsius = (value - 32) * 5 / 9
	case "K":
		celsius = value - 273.15
	default:
		return 0, fmt.Errorf("不支持的温度单位: %s", from)
	}
	switch strings.ToUpper(to) {
	case "C":
		return celsius, nil
	case "F":
		return celsius*9/5 + 32, nil
	case "K":
		return celsius + 273.15, nil
	default:
		return 0, fmt.Errorf("不支持的温度单位: %s", to)
	}
}

func (u *UnitConverterTool) Execute(args map[string]interface{}) (string, error) {
	valF, ok := args["value"].(float64)
	if !ok {
		return "", fmt.Errorf("value 参数错误")
	}
	from, _ := args["from_unit"].(string)
	to, _ := args["to_unit"].(string)
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return "", fmt.Errorf("单位不能为空")
	}
	fromU := strings.ToUpper(from)
	toU := strings.ToUpper(to)
	if fromU == toU {
		return fmt.Sprintf("%.4f %s", valF, to), nil
	}
	// 温度特殊处理
	if fromU == "C" || fromU == "F" || fromU == "K" || toU == "C" || toU == "F" || toU == "K" {
		res, err := convertTemp(valF, from, to)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%.4f %s", res, to), nil
	}
	// 一般单位
	if conv, ok := conversions[from]; ok {
		if factor, ok2 := conv[to]; ok2 {
			return fmt.Sprintf("%.4f %s", valF*factor, to), nil
		}
	}
	// 反向转换
	if conv, ok := conversions[to]; ok {
		if factor, ok2 := conv[from]; ok2 {
			return fmt.Sprintf("%.4f %s", valF/factor, to), nil
		}
	}
	return "", fmt.Errorf("不支持从 %s 到 %s 的转换", from, to)
}
