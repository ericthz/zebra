// tool/search.go
package tool

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type SearchTool struct{}

func (s *SearchTool) Name() string { return "web_search" }

func (s *SearchTool) Description() string {
	return "在互联网上搜索信息，返回摘要。"
}

func (s *SearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "搜索关键词",
			},
		},
		"required": []string{"query"},
	}
}

func (s *SearchTool) Execute(args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("缺少搜索关键词")
	}
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1", url.QueryEscape(query))
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Sprintf("搜索请求失败: %v", err), nil
	}
	defer resp.Body.Close()

	var data struct {
		Abstract string `json:"Abstract"`
		Heading  string `json:"Heading"`
	}
	json.NewDecoder(resp.Body).Decode(&data)
	if data.Abstract != "" {
		return fmt.Sprintf("%s: %s", data.Heading, data.Abstract), nil
	}
	return "未找到相关结果", nil
}
