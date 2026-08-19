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
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ericthz/zebra/internal/agent"
)

// readAudioPart 从 multipart 流式读取 file 字段与其他表单字段。
// 不用 ParseMultipartForm：它会把整个请求体先缓冲到内存（32MB 档），
// 再 ReadAll(f) 又复制一份，峰值内存翻倍（修复 P25 双重缓冲）。
// limitBytes 为音频大小上限；返回音频字节 + 其他文本字段。
// 注意：不能遇到 file 就提前 return，否则 file 之后的表单字段
// （如 session_id）会被丢弃——顺序无关地读完整个 multipart 流。
func readAudioPart(r *http.Request, limitBytes int64) ([]byte, map[string]string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, fmt.Errorf("multipart 解析失败: %w", err)
	}
	fields := make(map[string]string)
	var audio []byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if part.FormName() == "file" {
			audio, err = io.ReadAll(io.LimitReader(part, limitBytes+1))
			part.Close()
			if err != nil {
				return nil, nil, fmt.Errorf("读取音频失败: %w", err)
			}
			if int64(len(audio)) > limitBytes {
				return nil, nil, fmt.Errorf("音频超过大小上限 %d 字节", limitBytes)
			}
			continue
		}
		// 其他文本字段（如 session_id）：流式读取并记录
		b, err := io.ReadAll(io.LimitReader(part, 4096))
		part.Close()
		if err != nil {
			return nil, nil, err
		}
		fields[part.FormName()] = string(b)
	}
	if audio == nil {
		return nil, nil, fmt.Errorf("缺少 file 字段")
	}
	return audio, fields, nil
}

// handleVoiceTranscribe 语音转写：multipart 字段 file 上传音频。
func (s *APIServer) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.deps.Voice == nil {
		http.Error(w, "语音未启用（需配置 VOICE_BASE_URL）", http.StatusNotImplemented)
		return
	}
	audio, _, err := readAudioPart(r, 32<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	text, err := s.deps.Voice.Transcribe(r.Context(), audio, "", "")
	if err != nil {
		s.writeAgentError(w, "语音转写失败", err)
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
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
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
		s.writeAgentError(w, "语音合成失败", err)
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
	audio, fields, err := readAudioPart(r, 32<<20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 1. ASR：语音 → 文本
	text, err := s.deps.Voice.Transcribe(r.Context(), audio, "", "")
	if err != nil {
		s.writeAgentError(w, "语音转写失败", err)
		return
	}

	// 2. Agent：文本 → 回复（复用会话体系，支持多轮）
	// 锁内重取最新历史（六1）：Redis 会话存储下并发/连续请求不丢轮次。
	sess, err := s.lockSession(r.Context(), fields["session_id"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	defer sess.runMu.Unlock()
	ag := s.agentFor(sess)
	reply, err := ag.Run(r.Context(), text, agent.RunOptions{})
	if err != nil {
		// 失败轮次 Agent 已改写内存历史，仍须落库（六10，与 /v1/chat 对齐）
		s.persistHistory(sess)
		s.writeAgentError(w, "voice chat failed", err)
		return
	}
	// 语音链路同样要写回会话历史：否则 Redis 会话存储下多轮语音的
	// 上下文永不落库，下一轮对话丢失上文（与 /v1/chat 对齐）。
	s.persistHistory(sess)

	// 3. TTS：回复 → 音频
	audioBytes, _, err := s.deps.Voice.Synthesize(r.Context(), reply)
	if err != nil {
		s.writeAgentError(w, "语音合成失败", err)
		return
	}
	jsonOK(w, VoiceChatResponse{
		SessionID:   sess.ID,
		Text:        text,
		Reply:       reply,
		AudioBase64: base64.StdEncoding.EncodeToString(audioBytes),
	})
}
