// 语音交互：OpenAI 兼容 ASR（语音转写）+ TTS（语音合成）。
//
// 背景：语音是 Agent 的自然交互入口（耳机/APP/客服），但纯文本链路
// 接不进来。本文件用 OpenAI 兼容 HTTP API（whisper 转写 + tts 合成）
// 补齐"音频进 → 文本 → Agent → 文本 → 音频出"的全链路，仍保持零第三方依赖。
//
// 协议要点：
//
//	ASR：POST /audio/transcriptions，multipart/form-data（file + model）
//	TTS：POST /audio/speech，JSON（model/input/voice），响应为音频字节流
//
// 生产演化方向：流式 ASR（WebSocket）、多音色/多语言、音频缓存
// （相同文本不重复合成）；后端可换火山/阿里等任意 OpenAI 兼容网关。
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// VoiceClient OpenAI 兼容语音客户端。
type VoiceClient struct {
	BaseURL  string // 兼容网关基址（如 https://api.openai.com/v1）
	APIKey   string
	ASRModel string // 转写模型（默认 whisper-1）
	TTSModel string // 合成模型（默认 tts-1）
	Voice    string // 音色（alloy/echo/fable/onyx/nova/shimmer）
	Client   *http.Client
}

// Transcribe 语音转写：上传音频字节，返回识别文本。
// filename 建议带扩展名（.wav/.mp3），contentType 传音频 MIME（可空）。
func (v *VoiceClient) Transcribe(ctx context.Context, audio []byte, filename, contentType string) (string, error) {
	if len(audio) == 0 {
		return "", fmt.Errorf("音频为空")
	}
	if filename == "" {
		filename = "audio.wav"
	}
	if contentType == "" {
		contentType = "audio/wav"
	}

	// multipart/form-data：file 字段 + model 字段
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return "", err
	}
	if err := mw.WriteField("model", v.model()); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(v.BaseURL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	v.auth(req)

	resp, err := v.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("转写失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("转写响应解析失败: %w", err)
	}
	return out.Text, nil
}

// Synthesize 文本合成语音：返回音频字节与 MIME（如 audio/mpeg）。
func (v *VoiceClient) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, "", fmt.Errorf("合成文本为空")
	}
	payload, _ := json.Marshal(map[string]string{
		"model": v.ttsModel(), "input": text, "voice": v.voice(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(v.BaseURL, "/")+"/audio/speech", bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	v.auth(req)

	resp, err := v.client().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("合成失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	return audio, ct, nil
}

func (v *VoiceClient) client() *http.Client {
	if v.Client != nil {
		return v.Client
	}
	return http.DefaultClient
}

func (v *VoiceClient) auth(req *http.Request) {
	if v.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+v.APIKey)
	}
}

func (v *VoiceClient) model() string {
	if v.ASRModel == "" {
		return "whisper-1"
	}
	return v.ASRModel
}

func (v *VoiceClient) ttsModel() string {
	if v.TTSModel == "" {
		return "tts-1"
	}
	return v.TTSModel
}

func (v *VoiceClient) voice() string {
	if v.Voice == "" {
		return "alloy"
	}
	return v.Voice
}
