package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type OllamaOpenAIProvider struct {
	BaseURL string
	Model   string
	APIKey  string
}

func (p *OllamaOpenAIProvider) Chat(messages []Message, tools []Tool) (Message, error) {
	reqBody := struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
		Tools    []Tool    `json:"tools,omitempty"`
	}{
		Model:    p.Model,
		Messages: messages,
		Tools:    tools,
	}

	body, _ := json.Marshal(reqBody)
	httpReq, _ := http.NewRequest("POST", p.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("OpenAI 兼容接口错误 %s: %s", resp.Status, b)
	}

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}
	if len(result.Choices) == 0 {
		return Message{}, fmt.Errorf("OpenAI 兼容接口返回空 choices")
	}
	return result.Choices[0].Message, nil
}
