package tool

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type WeatherTool struct{}

func (w *WeatherTool) Name() string {
	return "get_current_weather"
}

func (w *WeatherTool) Description() string {
	return "获取指定城市的当前天气"
}

func (w *WeatherTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
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
	}
}

func (w *WeatherTool) Execute(args map[string]interface{}) (string, error) {
	location, _ := args["location"].(string)
	if location == "" {
		return "", fmt.Errorf("缺少参数 location")
	}
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1", url.PathEscape(location))
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Sprintf("查询天气失败: %v", err), nil
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
		return fmt.Sprintf("解析天气数据失败: %v", err), nil
	}
	if len(data.CurrentCondition) == 0 {
		return "未获取到天气信息", nil
	}
	cond := data.CurrentCondition[0]
	temp := cond.TempC
	desc := cond.WeatherDesc[0].Value
	return fmt.Sprintf("地点：%s，当前温度：%s°C，天气：%s", location, temp, desc), nil
}
