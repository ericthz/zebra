// tool/calculator.go
package tool

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type CalculatorTool struct{}

func (c *CalculatorTool) Name() string { return "calculator" }

func (c *CalculatorTool) Description() string {
	return "计算数学表达式，支持加减乘除、括号和小数。"
}

func (c *CalculatorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"expression": map[string]interface{}{
				"type":        "string",
				"description": "数学表达式，例如：(1+2)*3",
			},
		},
		"required": []string{"expression"},
	}
}

func (c *CalculatorTool) Execute(args map[string]interface{}) (string, error) {
	expr, _ := args["expression"].(string)
	if expr == "" {
		return "", fmt.Errorf("缺少表达式")
	}
	result, err := evaluateExpression(expr)
	if err != nil {
		return fmt.Sprintf("计算错误: %v", err), nil
	}
	return fmt.Sprintf("%v", result), nil
}

// ---- 简单的表达式求值器 (Shunting-yard) ----

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

func evaluateExpression(expr string) (float64, error) {
	tokens, err := tokenize(expr)
	if err != nil {
		return 0, err
	}
	var values []float64
	var ops []string

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
				if len(values) < 2 {
					return 0, fmt.Errorf("表达式错误")
				}
				b := values[len(values)-1]
				a := values[len(values)-2]
				values = values[:len(values)-2]
				op := ops[len(ops)-1]
				ops = ops[:len(ops)-1]
				res, err := applyOp(a, b, op)
				if err != nil {
					return 0, err
				}
				values = append(values, res)
			}
			if len(ops) == 0 {
				return 0, fmt.Errorf("括号不匹配")
			}
			ops = ops[:len(ops)-1] // pop '('
		case tkOp:
			for len(ops) > 0 && precedence(ops[len(ops)-1]) >= precedence(tok.value) {
				if len(values) < 2 {
					return 0, fmt.Errorf("表达式错误")
				}
				b := values[len(values)-1]
				a := values[len(values)-2]
				values = values[:len(values)-2]
				op := ops[len(ops)-1]
				ops = ops[:len(ops)-1]
				res, err := applyOp(a, b, op)
				if err != nil {
					return 0, err
				}
				values = append(values, res)
			}
			ops = append(ops, tok.value)
		}
	}

	for len(ops) > 0 {
		if len(values) < 2 {
			return 0, fmt.Errorf("表达式错误")
		}
		b := values[len(values)-1]
		a := values[len(values)-2]
		values = values[:len(values)-2]
		op := ops[len(ops)-1]
		ops = ops[:len(ops)-1]
		res, err := applyOp(a, b, op)
		if err != nil {
			return 0, err
		}
		values = append(values, res)
	}

	if len(values) != 1 {
		return 0, fmt.Errorf("表达式错误")
	}
	if math.IsInf(values[0], 0) || math.IsNaN(values[0]) {
		return 0, fmt.Errorf("计算结果溢出")
	}
	return values[0], nil
}
