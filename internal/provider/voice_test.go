package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newVoiceMock 模拟 OpenAI 兼容语音网关：断言请求格式并返回固定结果。
func newVoiceMock(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// ASR：校验 multipart 包含 file 与 model，返回 {"text": "..."}
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("转写请求格式错误: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("缺少 file 字段: %v", err)
			return
		}
		f.Close()
		if r.FormValue("model") == "" {
			t.Error("缺少 model 字段")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("鉴权头错误: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"北京今天天气怎么样？"}`)
	})

	// TTS：校验 JSON 请求体，返回音频字节
	mux.HandleFunc("/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["model"] == "" || body["input"] == "" || body["voice"] == "" {
			t.Errorf("TTS 请求体缺字段: %v", body)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "FAKE-MP3-DATA")
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestVoiceTranscribe(t *testing.T) {
	ts := newVoiceMock(t)
	vc := &VoiceClient{BaseURL: ts.URL, APIKey: "test-key", ASRModel: "whisper-1", Client: ts.Client()}

	text, err := vc.Transcribe(context.Background(), []byte("RIFF....WAV"), "input.wav", "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "北京") {
		t.Fatalf("转写文本异常: %q", text)
	}
	if _, err := vc.Transcribe(context.Background(), nil, "", ""); err == nil {
		t.Fatal("空音频应报错")
	}
}

func TestVoiceSynthesize(t *testing.T) {
	ts := newVoiceMock(t)
	vc := &VoiceClient{BaseURL: ts.URL, APIKey: "test-key", TTSModel: "tts-1", Voice: "nova", Client: ts.Client()}

	audio, ct, err := vc.Synthesize(context.Background(), "你好，今天天气不错。")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(audio, []byte("FAKE-MP3-DATA")) || ct != "audio/mpeg" {
		t.Fatalf("合成结果异常: %q %q", audio, ct)
	}
	if _, _, err := vc.Synthesize(context.Background(), "   "); err == nil {
		t.Fatal("空文本应报错")
	}
}
