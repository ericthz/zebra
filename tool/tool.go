package tool

import (
	"encoding/json"
	"fmt"

	"github.com/ericthz/aidemo/provider"
)

/*
		工具名称					描述
	get_current_weather		获取指定城市的当前天气
	calculator				计算数学表达式，支持加减乘除、括号和小数
	get_current_datetime	获取当前日期和时间，返回 ISO 8601 格式
	generate_random_number	生成指定范围内的随机整数（包含 min 和 max）
	web_search				在互联网上搜索信息，返回摘要
	convert_units			在不同单位之间进行转换，支持长度、重量、温度等
	translate_text			将文本从一种语言翻译到另一种语言
	get_ip_info				获取 IP 地址的地理位置信息，不提供 IP 则查询本机公网 IP
*/

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(args map[string]interface{}) (string, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) ToProviderTools() []provider.Tool {
	var tools []provider.Tool
	for _, t := range r.tools {
		tools = append(tools, provider.Tool{
			Type: "function",
			Function: provider.FunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return tools
}

func (r *Registry) Execute(name string, args map[string]interface{}) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("未找到工具: %s", name)
	}
	return t.Execute(args)
}

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
		return nil, fmt.Errorf("arguments 字符串内容无效: %v", err)
	}
	return args, nil
}
