// 内置网络工具：天气、搜索、翻译、IP 信息。
// 均通过第三方免费 API 实现，演示"Agent 访问外部世界"的工具形态。
// 注意：真实生产应把这类工具做成可观测、限流、注入防护的（见 safety 包）。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// netClient 网络工具共用的 HTTP 客户端（带超时，避免永久阻塞）。
var netClient = &http.Client{Timeout: 8 * time.Second}

// ---------------- 天气 ----------------

// WeatherTool 天气查询（wttr.in）。
type WeatherTool struct{}

func (w *WeatherTool) Name() string { return "get_current_weather" }
func (w *WeatherTool) Description() string {
	return "获取指定城市的当前天气"
}
func (w *WeatherTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"location": map[string]interface{}{"type": "string", "description": "城市名称，例如：北京"},
			"unit":     map[string]interface{}{"type": "string", "enum": []string{"celsius", "fahrenheit"}},
		},
		"required": []string{"location"},
	}
}
func (w *WeatherTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	loc := StringArg(args, "location")
	if loc == "" {
		return "", fmt.Errorf("缺少参数 location")
	}
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1", url.PathEscape(loc))
	resp, err := netClient.Get(apiURL)
	if err != nil {
		return "", err
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
		return "", err
	}
	if len(data.CurrentCondition) == 0 {
		return "未获取到天气信息", nil
	}
	c := data.CurrentCondition[0]
	desc := ""
	if len(c.WeatherDesc) > 0 {
		desc = c.WeatherDesc[0].Value
	}
	return fmt.Sprintf("地点：%s，当前温度：%s°C，天气：%s", loc, c.TempC, desc), nil
}

// ---------------- 搜索 ----------------

// SearchTool 网页搜索（DuckDuckGo 摘要）。
type SearchTool struct{}

func (s *SearchTool) Name() string { return "web_search" }
func (s *SearchTool) Description() string {
	return "在互联网上搜索信息，返回摘要。"
}
func (s *SearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "搜索关键词"},
		},
		"required": []string{"query"},
	}
}
func (s *SearchTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	q := StringArg(args, "query")
	if q == "" {
		return "", fmt.Errorf("缺少搜索关键词")
	}
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1", url.QueryEscape(q))
	resp, err := netClient.Get(apiURL)
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

// ---------------- 翻译 ----------------

// TranslateTool 文本翻译（MyMemory 免费 API）。
type TranslateTool struct{}

func (t *TranslateTool) Name() string { return "translate_text" }
func (t *TranslateTool) Description() string {
	return "将文本从一种语言翻译到另一种语言。"
}
func (t *TranslateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text":        map[string]interface{}{"type": "string", "description": "要翻译的文本"},
			"source_lang": map[string]interface{}{"type": "string", "description": "源语言代码，例如：en, zh, fr，auto 表示自动检测"},
			"target_lang": map[string]interface{}{"type": "string", "description": "目标语言代码，例如：en, zh, es"},
		},
		"required": []string{"text", "target_lang"},
	}
}
func (t *TranslateTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	text := StringArg(args, "text")
	target := StringArg(args, "target_lang")
	source := StringArg(args, "source_lang")
	if text == "" || target == "" {
		return "", fmt.Errorf("缺少文本或目标语言")
	}
	langPair := target
	if source != "" {
		langPair = source + "|" + target
	}
	apiURL := fmt.Sprintf("https://api.mymemory.translated.net/get?q=%s&langpair=%s",
		url.QueryEscape(text), url.QueryEscape(langPair))
	resp, err := netClient.Get(apiURL)
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
	if result.ResponseData.TranslatedText != "" {
		return result.ResponseData.TranslatedText, nil
	}
	return "翻译服务未返回结果", nil
}

// ---------------- IP 信息 ----------------

// IPInfoTool IP 地理位置查询（ip-api.com 免费接口）。
type IPInfoTool struct{}

func (i *IPInfoTool) Name() string { return "get_ip_info" }
func (i *IPInfoTool) Description() string {
	return "获取 IP 地址的地理位置信息，不提供 IP 则查询本机公网 IP。"
}
func (i *IPInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"ip": map[string]interface{}{"type": "string", "description": "IP 地址（可选）"},
		},
	}
}
func (i *IPInfoTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	ip := StringArg(args, "ip")
	if ip == "" {
		resp, err := netClient.Get("https://api.ipify.org?format=json")
		if err != nil {
			return "", err
		}
		var ipData struct {
			IP string `json:"ip"`
		}
		json.NewDecoder(resp.Body).Decode(&ipData)
		resp.Body.Close()
		ip = ipData.IP
		if ip == "" {
			return "无法获取公网 IP", nil
		}
	}
	geoURL := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,message,country,regionName,city,zip,lat,lon,isp,query", ip)
	resp, err := netClient.Get(geoURL)
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
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		return "", err
	}
	if geo.Status != "success" {
		return fmt.Sprintf("查询失败: %s", geo.Message), nil
	}
	return fmt.Sprintf("IP: %s\n国家: %s\n地区: %s\n城市: %s\n邮编: %s\n经纬度: %.4f, %.4f\nISP: %s",
		geo.Query, geo.Country, geo.Region, geo.City, geo.Zip, geo.Lat, geo.Lon, geo.ISP), nil
}
