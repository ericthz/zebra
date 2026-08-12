package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type OllamaNativeProvider struct {
	BaseURL string
	Model   string
}

func (p *OllamaNativeProvider) Chat(messages []Message, tools []Tool) (Message, error) {
	reqBody := struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
		Stream   bool      `json:"stream"`
		Tools    []Tool    `json:"tools,omitempty"`
	}{
		Model:    p.Model,
		Messages: messages,
		Stream:   false,
		Tools:    tools,
	}

	body, _ := json.Marshal(reqBody)
	httpReq, _ := http.NewRequest("POST", p.BaseURL+"/api/chat", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return Message{}, fmt.Errorf("Ollama 原生接口错误 %s: %s", resp.Status, b)
	}

	var result struct {
		Message Message `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Message{}, err
	}
	return result.Message, nil
}
