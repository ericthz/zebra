// 内置本地工具：计算器、日期时间、随机数、单位换算。
// 计算器使用手写 Shunting-yard（调度场算法）表达式求值器。
package tool

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ---------------- 计算器 ----------------

// CalculatorTool 数学表达式计算。
type CalculatorTool struct{}

func (c *CalculatorTool) Name() string { return "calculator" }
func (c *CalculatorTool) Description() string {
	return "计算数学表达式，支持加减乘除、括号和小数。"
}
func (c *CalculatorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"expression": map[string]interface{}{"type": "string", "description": "数学表达式，例如：(1+2)*3"},
		},
		"required": []string{"expression"},
	}
}
func (c *CalculatorTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	expr := StringArg(args, "expression")
	if expr == "" {
		return "", fmt.Errorf("缺少表达式")
	}
	v, err := evaluateExpression(expr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", v), nil
}

type tokenKind int

const (
	tkNumber tokenKind = iota
	tkOp
	tkLParen
	tkRParen
)

type token struct {
	kind  tokenKind
	value string
}

func tokenize(expr string) ([]token, error) {
	var tokens []token
	expr = strings.ReplaceAll(expr, " ", "")
	i := 0
	for i < len(expr) {
		c := expr[i]
		switch {
		case c >= '0' && c <= '9' || c == '.':
			j := i
			for j < len(expr) && (expr[j] >= '0' && expr[j] <= '9' || expr[j] == '.') {
				j++
			}
			tokens = append(tokens, token{tkNumber, expr[i:j]})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/':
			tokens = append(tokens, token{tkOp, string(c)})
			i++
		case c == '(':
			tokens = append(tokens, token{tkLParen, "("})
			i++
		case c == ')':
			tokens = append(tokens, token{tkRParen, ")"})
			i++
		default:
			return nil, fmt.Errorf("非法字符: %c", c)
		}
	}
	return tokens, nil
}

func precedence(op string) int {
	switch op {
	case "+", "-":
		return 1
	case "*", "/":
		return 2
	}
	return 0
}

func applyOp(a, b float64, op string) (float64, error) {
	switch op {
	case "+":
		return a + b, nil
	case "-":
		return a - b, nil
	case "*":
		return a * b, nil
	case "/":
		if b == 0 {
			return 0, fmt.Errorf("除数不能为0")
		}
		return a / b, nil
	}
	return 0, fmt.Errorf("未知运算符")
}

// evaluateExpression 调度场算法：中缀 → 后缀求值。
func evaluateExpression(expr string) (float64, error) {
	tokens, err := tokenize(expr)
	if err != nil {
		return 0, err
	}
	var values []float64
	var ops []string

	reduce := func() error {
		if len(values) < 2 {
			return fmt.Errorf("表达式错误")
		}
		b := values[len(values)-1]
		a := values[len(values)-2]
		values = values[:len(values)-2]
		op := ops[len(ops)-1]
		ops = ops[:len(ops)-1]
		res, err := applyOp(a, b, op)
		if err != nil {
			return err
		}
		values = append(values, res)
		return nil
	}

	for _, tok := range tokens {
		switch tok.kind {
		case tkNumber:
			val, err := strconv.ParseFloat(tok.value, 64)
			if err != nil {
				return 0, err
			}
			values = append(values, val)
		case tkLParen:
			ops = append(ops, "(")
		case tkRParen:
			for len(ops) > 0 && ops[len(ops)-1] != "(" {
				if err := reduce(); err != nil {
					return 0, err
				}
			}
			if len(ops) == 0 {
				return 0, fmt.Errorf("括号不匹配")
			}
			ops = ops[:len(ops)-1]
		case tkOp:
			for len(ops) > 0 && precedence(ops[len(ops)-1]) >= precedence(tok.value) {
				if err := reduce(); err != nil {
					return 0, err
				}
			}
			ops = append(ops, tok.value)
		}
	}
	for len(ops) > 0 {
		if err := reduce(); err != nil {
			return 0, err
		}
	}
	if len(values) != 1 {
		return 0, fmt.Errorf("表达式错误")
	}
	v := values[0]
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("计算结果溢出")
	}
	return v, nil
}

// ---------------- 日期时间 ----------------

// DateTimeTool 当前日期时间。
type DateTimeTool struct{}

func (d *DateTimeTool) Name() string { return "get_current_datetime" }
func (d *DateTimeTool) Description() string {
	return "获取当前日期和时间，返回 ISO 8601 格式。"
}
func (d *DateTimeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (d *DateTimeTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return time.Now().Format("2006-01-02 15:04:05 Monday"), nil
}

// ---------------- 随机数 ----------------

// RandomTool 指定范围内随机整数。
type RandomTool struct{}

func (r *RandomTool) Name() string { return "generate_random_number" }
func (r *RandomTool) Description() string {
	return "生成指定范围内的随机整数（包含 min 和 max）。"
}
func (r *RandomTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"min": map[string]interface{}{"type": "integer", "description": "最小值"},
			"max": map[string]interface{}{"type": "integer", "description": "最大值"},
		},
		"required": []string{"min", "max"},
	}
}
func (r *RandomTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	minF, ok := FloatArg(args, "min")
	if !ok {
		return "", fmt.Errorf("min 参数错误")
	}
	maxF, ok := FloatArg(args, "max")
	if !ok {
		return "", fmt.Errorf("max 参数错误")
	}
	min, max := int(minF), int(maxF)
	if min > max {
		return "", fmt.Errorf("min 不能大于 max")
	}
	return fmt.Sprintf("%d", time.Now().UnixNano()%int64(max-min+1)+int64(min)), nil
}

// ---------------- 单位换算 ----------------

// UnitConverterTool 长度/重量/温度单位换算。
type UnitConverterTool struct{}

func (u *UnitConverterTool) Name() string { return "convert_units" }
func (u *UnitConverterTool) Description() string {
	return "在不同单位之间进行转换，支持长度、重量、温度等。"
}
func (u *UnitConverterTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"value":     map[string]interface{}{"type": "number", "description": "待转换的数值"},
			"from_unit": map[string]interface{}{"type": "string", "description": "源单位，例如：km, mi, kg, lb, C, F"},
			"to_unit":   map[string]interface{}{"type": "string", "description": "目标单位，例如：m, ft, g, oz, K"},
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
}

func (u *UnitConverterTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	val, ok := FloatArg(args, "value")
	if !ok {
		return "", fmt.Errorf("value 参数错误")
	}
	from := strings.ToUpper(StringArg(args, "from_unit"))
	to := strings.ToUpper(StringArg(args, "to_unit"))
	if from == "" || to == "" {
		return "", fmt.Errorf("单位不能为空")
	}
	if from == to {
		return fmt.Sprintf("%.4f %s", val, to), nil
	}
	if from == "C" || from == "F" || from == "K" || to == "C" || to == "F" || to == "K" {
		v, err := convertTemp(val, from, to)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%.4f %s", v, to), nil
	}
	if conv, ok := conversions[from]; ok {
		if f, ok2 := conv[to]; ok2 {
			return fmt.Sprintf("%.4f %s", val*f, to), nil
		}
	}
	if conv, ok := conversions[to]; ok {
		if f, ok2 := conv[from]; ok2 {
			return fmt.Sprintf("%.4f %s", val/f, to), nil
		}
	}
	return "", fmt.Errorf("不支持从 %s 到 %s 的转换", from, to)
}

func convertTemp(value float64, from, to string) (float64, error) {
	var celsius float64
	switch from {
	case "C":
		celsius = value
	case "F":
		celsius = (value - 32) * 5 / 9
	case "K":
		celsius = value - 273.15
	default:
		return 0, fmt.Errorf("不支持的温度单位: %s", from)
	}
	switch to {
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
