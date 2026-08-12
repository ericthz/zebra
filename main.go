package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ericthz/aidemo/agent"
	"github.com/ericthz/aidemo/config"
	"github.com/ericthz/aidemo/provider"
	"github.com/ericthz/aidemo/tool"
)

func main() {
	if err := config.LoadEnv(".env"); err != nil {
		log.Println("未找到 .env 文件，使用默认配置")
	}

	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "llama3.1"
	}
	providerName := strings.ToLower(os.Getenv("OLLAMA_PROVIDER"))
	if providerName == "" {
		providerName = "ollama"
	}
	apiKey := os.Getenv("OLLAMA_API_KEY")

	var prov provider.Provider
	switch providerName {
	case "ollama", "native":
		prov = &provider.OllamaNativeProvider{BaseURL: baseURL, Model: model}
	case "openai":
		prov = &provider.OllamaOpenAIProvider{BaseURL: baseURL, Model: model, APIKey: apiKey}
	case "anthropic":
		prov = &provider.OllamaAnthropicProvider{BaseURL: baseURL, Model: model, APIKey: apiKey}
	default:
		log.Fatalf("不支持的接口类型: %s", providerName)
	}

	/*
		北京和上海今天天气怎么样？
		计算 (23 + 19) * 5 等于多少？
		现在几点？
		生成一个 1 到 100 的随机数
		搜索人工智能的最新进展
		将 100 公里转换为英里
		将 ‘Hello world’ 翻译成中文
		查询 8.8.8.8 的地理位置
	*/
	reg := tool.NewRegistry()
	reg.Register(&tool.WeatherTool{})
	reg.Register(&tool.CalculatorTool{})
	reg.Register(&tool.DateTimeTool{})
	reg.Register(&tool.RandomTool{})
	reg.Register(&tool.SearchTool{})
	reg.Register(&tool.UnitConverterTool{})
	reg.Register(&tool.TranslateTool{})
	reg.Register(&tool.IPInfoTool{})

	ag := agent.NewAgent(prov, reg, 5)

	userInput := "现在几点？"
	log.Printf("🚀 用户输入：%s\n", userInput)
	answer, err := ag.Run(userInput)
	if err != nil {
		log.Fatalf("Agent 错误: %v", err)
	}
	fmt.Println("🤖 最终回答：", answer)
}
