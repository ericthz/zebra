// 语音交互 API（P25）：
//
//	POST /v1/voice/transcribe  音频 → 文本（multipart 上传）
//	POST /v1/voice/synthesize  文本 → 音频（返回音频字节流）
//	POST /v1/voice/chat        音频 → Agent 对话 → 音频回复（JSON + audio_base64）
//
// 语音入口面向所有已认证用户（与 /v1/chat 同级）；后端为 OpenAI 兼容
// 网关（whisper 转写 + tts 合成），见 internal/provider/voice.go。
package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/ericthz/zebra/internal/agent"
)

// handleVoiceTranscribe 语音转写：multipart 字段 file 上传音频。
func (s *APIServer) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.deps.Voice == nil {
		http.Error(w, "语音未启用（需配置 VOICE_BASE_URL）", http.StatusNotImplemented)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "multipart 解析失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "缺少 file 字段", http.StatusBadRequest)
		return
	}
	defer f.Close()
	audio, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil {
		http.Error(w, "读取音频失败", http.StatusBadRequest)
		return
	}
	text, err := s.deps.Voice.Transcribe(r.Context(), audio, "", "")
	if err != nil {
		s.deps.Logger.Warn("语音转写失败", "err", err)
		http.Error(w, "transcribe error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"text": text})
}

// SynthesizeRequest 文本合成语音请求体。
type SynthesizeRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice,omitempty"` // 音色覆盖（可选）
}

// handleVoiceSynthesize 文本合成语音：直接返回音频字节流。
func (s *APIServer) handleVoiceSynthesize(w http.ResponseWriter, r *http.Request) {
	if s.deps.Voice == nil {
		http.Error(w, "语音未启用（需配置 VOICE_BASE_URL）", http.StatusNotImplemented)
		return
	}
	var req SynthesizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "bad request: text 必填", http.StatusBadRequest)
		return
	}
	var (
		audio []byte
		ct    string
		err   error
	)
	if req.Voice != "" {
		// 单次请求音色覆盖：拷贝一个临时客户端，不改共享配置（并发安全）
		vc := *s.deps.Voice
		vc.Voice = req.Voice
		audio, ct, err = vc.Synthesize(r.Context(), req.Text)
	} else {
		audio, ct, err = s.deps.Voice.Synthesize(r.Context(), req.Text)
	}
	if err != nil {
		s.deps.Logger.Warn("语音合成失败", "err", err)
		http.Error(w, "synthesize error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Write(audio)
}

// VoiceChatRequest 语音对话：multipart file = 用户语音。
type VoiceChatRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

// VoiceChatResponse 语音对话返回：文本 + 回复音频（base64）。
type VoiceChatResponse struct {
	SessionID   string `json:"session_id"`
	Text        string `json:"text"`         // 识别出的用户问题
	Reply       string `json:"reply"`        // Agent 文本回复
	AudioBase64 string `json:"audio_base64"` // 回复音频（调用方 base64 解码播放）
}

// handleVoiceChat 语音全链路：转写 → Agent 对话 → 合成回复音频。
func (s *APIServer) handleVoiceChat(w http.ResponseWriter, r *http.Request) {
	if s.deps.Voice == nil {
		http.Error(w, "语音未启用（需配置 VOICE_BASE_URL）", http.StatusNotImplemented)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "multipart 解析失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "缺少 file 字段", http.StatusBadRequest)
		return
	}
	defer f.Close()
	audio, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil {
		http.Error(w, "读取音频失败", http.StatusBadRequest)
		return
	}

	// 1. ASR：语音 → 文本
	text, err := s.deps.Voice.Transcribe(r.Context(), audio, "", "")
	if err != nil {
		http.Error(w, "transcribe error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2. Agent：文本 → 回复（复用会话体系，支持多轮）
	sess, err := s.sessionFor(r.Context(), r.FormValue("session_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	ag := s.agentFor(sess)
	reply, err := ag.Run(r.Context(), text, agent.RunOptions{})
	if err != nil {
		http.Error(w, "agent error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. TTS：回复 → 音频
	audioBytes, _, err := s.deps.Voice.Synthesize(r.Context(), reply)
	if err != nil {
		http.Error(w, "synthesize error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, VoiceChatResponse{
		SessionID:   sess.ID,
		Text:        text,
		Reply:       reply,
		AudioBase64: base64.StdEncoding.EncodeToString(audioBytes),
	})
}
