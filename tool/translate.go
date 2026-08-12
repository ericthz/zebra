// tool/translate.go
package tool

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type TranslateTool struct{}

func (t *TranslateTool) Name() string { return "translate_text" }

func (t *TranslateTool) Description() string {
	return "将文本从一种语言翻译到另一种语言。"
}

func (t *TranslateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
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
	}
}

func (t *TranslateTool) Execute(args map[string]interface{}) (string, error) {
	text, _ := args["text"].(string)
	target, _ := args["target_lang"].(string)
	source, _ := args["source_lang"].(string)
	if text == "" || target == "" {
		return "", fmt.Errorf("缺少文本或目标语言")
	}
	langPair := target
	if source != "" {
		langPair = source + "|" + target
	}
	apiURL := fmt.Sprintf("https://api.mymemory.translated.net/get?q=%s&langpair=%s", url.QueryEscape(text), url.QueryEscape(langPair))
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Sprintf("翻译请求失败: %v", err), nil
	}
	defer resp.Body.Close()
	var result struct {
		ResponseData struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.ResponseData.TranslatedText != "" {
		return result.ResponseData.TranslatedText, nil
	}
	return "翻译服务未返回结果", nil
}
