// mcp_server.go
// 集成全部 AI Agent 工具的 MCP 服务器
// 支持的工具：
//   get_current_weather    - 天气查询 (wttr.in)
//   calculator             - 数学表达式计算
//   get_current_datetime   - 当前日期时间
//   generate_random_number - 随机数生成
//   web_search             - DuckDuckGo 搜索
//   convert_units          - 单位换算 (长度/重量/温度)
//   translate_text         - 文本翻译 (MyMemory)
//   get_ip_info            - IP 信息查询 (ip-api.com)
//
// 运行方式：
//   stdio 模式（默认）：go run mcp_server.go
//   HTTP 模式：         go run mcp_server.go -http :9000
//
// 与主程序 main.go 配合时，MCP 工具会覆盖同名的本地工具。

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ericthz/aidemo/mcp"
)

// -------------------- 工具函数实现 --------------------

// ---- 天气查询 ----
func getCurrentWeather(location, unit string) string {
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1", url.PathEscape(location))
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Sprintf("查询天气失败: %v", err)
	}
	defer resp.Body.Close()

	var data struct {
		CurrentCondition []struct {
			TempC       string `json:"temp_C"`
			WeatherDesc []struct {
				Value string `json:"value"`
			} `json:"weatherDesc"`
		} `json:"current_condition"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Sprintf("解析天气数据失败: %v", err)
	}
	if len(data.CurrentCondition) == 0 {
		return "未获取到天气信息"
	}
	cond := data.CurrentCondition[0]
	temp := cond.TempC
	desc := cond.WeatherDesc[0].Value
	return fmt.Sprintf("地点：%s，当前温度：%s°C，天气：%s", location, temp, desc)
}

// ---- 数学表达式计算 ----
func evaluateExpression(expr string) (float64, error) {
	// 简单的表达式求值器，支持 + - * / 和括号
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
	tokenize := func(s string) ([]token, error) {
		var tokens []token
		s = strings.ReplaceAll(s, " ", "")
		i := 0
		for i < len(s) {
			c := s[i]
			switch {
			case (c >= '0' && c <= '9') || c == '.':
				j := i
				for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
					j++
				}
				tokens = append(tokens, token{tkNumber, s[i:j]})
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

	precedence := func(op string) int {
		switch op {
		case "+", "-":
			return 1
		case "*", "/":
			return 2
		}
		return 0
	}

	applyOp := func(a, b float64, op string) (float64, error) {
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

// ---- 随机数 ----
func generateRandom(min, max int) (int, error) {
	if min > max {
		return 0, fmt.Errorf("min 不能大于 max")
	}
	return rand.Intn(max-min+1) + min, nil
}

// ---- DuckDuckGo 搜索 ----
func webSearch(query string) (string, error) {
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1", url.QueryEscape(query))
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var data struct {
		Abstract string `json:"Abstract"`
		Heading  string `json:"Heading"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if data.Abstract != "" {
		return fmt.Sprintf("%s: %s", data.Heading, data.Abstract), nil
	}
	return "未找到相关结果", nil
}

// ---- 单位换算 ----
func convertUnits(value float64, from, to string) (string, error) {
	fromU := strings.ToUpper(strings.TrimSpace(from))
	toU := strings.ToUpper(strings.TrimSpace(to))

	// 温度特殊处理
	if fromU == "C" || fromU == "F" || fromU == "K" || toU == "C" || toU == "F" || toU == "K" {
		var celsius float64
		switch fromU {
		case "C":
			celsius = value
		case "F":
			celsius = (value - 32) * 5 / 9
		case "K":
			celsius = value - 273.15
		default:
			return "", fmt.Errorf("不支持的温度单位: %s", from)
		}
		var result float64
		switch toU {
		case "C":
			result = celsius
		case "F":
			result = celsius*9/5 + 32
		case "K":
			result = celsius + 273.15
		default:
			return "", fmt.Errorf("不支持的温度单位: %s", to)
		}
		return fmt.Sprintf("%.4f %s", result, to), nil
	}

	// 长度/重量等
	conversions := map[string]map[string]float64{
		"KM": {"M": 1000, "MI": 0.621371, "FT": 3280.84},
		"M":  {"KM": 0.001, "CM": 100, "FT": 3.28084},
		"MI": {"KM": 1.60934, "M": 1609.34, "FT": 5280},
		"KG": {"G": 1000, "LB": 2.20462, "OZ": 35.274},
		"G":  {"KG": 0.001, "OZ": 0.035274},
		"LB": {"KG": 0.453592, "G": 453.592, "OZ": 16},
	}

	if conv, ok := conversions[fromU]; ok {
		if factor, ok2 := conv[toU]; ok2 {
			return fmt.Sprintf("%.4f %s", value*factor, to), nil
		}
	}
	// 反向
	if conv, ok := conversions[toU]; ok {
		if factor, ok2 := conv[fromU]; ok2 {
			return fmt.Sprintf("%.4f %s", value/factor, to), nil
		}
	}
	return "", fmt.Errorf("不支持从 %s 到 %s 的转换", from, to)
}

// ---- 翻译 ----
func translateText(text, sourceLang, targetLang string) (string, error) {
	langPair := targetLang
	if sourceLang != "" {
		langPair = sourceLang + "|" + targetLang
	}
	apiURL := fmt.Sprintf("https://api.mymemory.translated.net/get?q=%s&langpair=%s",
		url.QueryEscape(text), url.QueryEscape(langPair))
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		ResponseData struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.ResponseData.TranslatedText == "" {
		return "翻译服务未返回结果", nil
	}
	return result.ResponseData.TranslatedText, nil
}

// ---- IP 信息 ----
func getIPInfo(ip string) (string, error) {
	if ip == "" {
		// 获取公网 IP
		resp, err := http.Get("https://api.ipify.org?format=json")
		if err != nil {
			return "", fmt.Errorf("获取公网 IP 失败: %v", err)
		}
		defer resp.Body.Close()
		var ipData struct {
			IP string `json:"ip"`
		}
		json.NewDecoder(resp.Body).Decode(&ipData)
		ip = ipData.IP
	}
	geoURL := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,message,country,regionName,city,zip,lat,lon,isp,query", ip)
	resp, err := http.Get(geoURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var geo struct {
		Status  string  `json:"status"`
		Message string  `json:"message"`
		Country string  `json:"country"`
		Region  string  `json:"regionName"`
		City    string  `json:"city"`
		Zip     string  `json:"zip"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
		ISP     string  `json:"isp"`
		Query   string  `json:"query"`
	}
	json.NewDecoder(resp.Body).Decode(&geo)
	if geo.Status != "success" {
		return "", fmt.Errorf("查询失败: %s", geo.Message)
	}
	return fmt.Sprintf("IP: %s\n国家: %s\n地区: %s\n城市: %s\n邮编: %s\n经纬度: %.4f, %.4f\nISP: %s",
		geo.Query, geo.Country, geo.Region, geo.City, geo.Zip, geo.Lat, geo.Lon, geo.ISP), nil
}

// -------------------- 主函数 --------------------
func main() {
	httpAddr := flag.String("http", "", "HTTP 监听地址，例如 ':9000'")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())
	srv := mcp.NewServer()

	// 注册 get_current_weather
	srv.RegisterTool(mcp.ToolDef{
		Name:        "get_current_weather",
		Description: "获取指定城市的当前天气",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"location": map[string]interface{}{
					"type":        "string",
					"description": "城市名称，例如：北京",
				},
				"unit": map[string]interface{}{
					"type": "string",
					"enum": []string{"celsius", "fahrenheit"},
				},
			},
			"required": []string{"location"},
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		location, _ := args["location"].(string)
		unit, _ := args["unit"].(string)
		if unit == "" {
			unit = "celsius"
		}
		result := getCurrentWeather(location, unit)
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: result}},
		}, nil
	})

	// 注册 calculator
	srv.RegisterTool(mcp.ToolDef{
		Name:        "calculator",
		Description: "计算数学表达式，支持加减乘除、括号和小数。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"expression": map[string]interface{}{
					"type":        "string",
					"description": "数学表达式，例如：(1+2)*3",
				},
			},
			"required": []string{"expression"},
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		expr, _ := args["expression"].(string)
		res, err := evaluateExpression(expr)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: fmt.Sprintf("%v", res)}},
		}, nil
	})

	// 注册 get_current_datetime
	srv.RegisterTool(mcp.ToolDef{
		Name:        "get_current_datetime",
		Description: "获取当前日期和时间",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		now := time.Now().Format("2006-01-02 15:04:05 Monday")
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: now}},
		}, nil
	})

	// 注册 generate_random_number
	srv.RegisterTool(mcp.ToolDef{
		Name:        "generate_random_number",
		Description: "生成指定范围内的随机整数（包含 min 和 max）",
		InputSchema: map[string]interface{}{
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
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		minF, ok := args["min"].(float64)
		if !ok {
			return nil, fmt.Errorf("min 参数错误")
		}
		maxF, ok := args["max"].(float64)
		if !ok {
			return nil, fmt.Errorf("max 参数错误")
		}
		min := int(minF)
		max := int(maxF)
		res, err := generateRandom(min, max)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: fmt.Sprintf("%d", res)}},
		}, nil
	})

	// 注册 web_search
	srv.RegisterTool(mcp.ToolDef{
		Name:        "web_search",
		Description: "在互联网上搜索信息，返回摘要。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "搜索关键词",
				},
			},
			"required": []string{"query"},
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		query, _ := args["query"].(string)
		res, err := webSearch(query)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: res}},
		}, nil
	})

	// 注册 convert_units
	srv.RegisterTool(mcp.ToolDef{
		Name:        "convert_units",
		Description: "在不同单位之间进行转换，支持长度、重量、温度等。",
		InputSchema: map[string]interface{}{
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
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		valF, ok := args["value"].(float64)
		if !ok {
			return nil, fmt.Errorf("value 参数错误")
		}
		from, _ := args["from_unit"].(string)
		to, _ := args["to_unit"].(string)
		res, err := convertUnits(valF, from, to)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: res}},
		}, nil
	})

	// 注册 translate_text
	srv.RegisterTool(mcp.ToolDef{
		Name:        "translate_text",
		Description: "将文本从一种语言翻译到另一种语言。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{
					"type":        "string",
					"description": "要翻译的文本",
				},
				"source_lang": map[string]interface{}{
					"type":        "string",
					"description": "源语言代码，例如：en, zh, fr，auto 表示自动检测",
				},
				"target_lang": map[string]interface{}{
					"type":        "string",
					"description": "目标语言代码，例如：en, zh, es",
				},
			},
			"required": []string{"text", "target_lang"},
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		text, _ := args["text"].(string)
		target, _ := args["target_lang"].(string)
		source, _ := args["source_lang"].(string)
		res, err := translateText(text, source, target)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: res}},
		}, nil
	})

	// 注册 get_ip_info
	srv.RegisterTool(mcp.ToolDef{
		Name:        "get_ip_info",
		Description: "获取 IP 地址的地理位置信息，不提供 IP 则查询本机公网 IP。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"ip": map[string]interface{}{
					"type":        "string",
					"description": "IP 地址（可选）",
				},
			},
		},
	}, func(args map[string]interface{}) (*mcp.CallToolResult, error) {
		ip, _ := args["ip"].(string)
		res, err := getIPInfo(ip)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: res}},
		}, nil
	})

	// 启动服务器
	if *httpAddr != "" {
		log.Printf("🚀 MCP 服务器 (全工具) HTTP 模式，监听 %s\n", *httpAddr)
		if err := srv.ServeHTTP(*httpAddr); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Println("🚀 MCP 服务器 (全工具) stdio 模式，等待连接...")
		if err := srv.ServeStdio(); err != nil {
			log.Fatal(err)
		}
	}
}
