package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ericthz/zebra/internal/prompt"
	"github.com/ericthz/zebra/internal/provider"
	"github.com/ericthz/zebra/internal/redis"
	"github.com/ericthz/zebra/internal/redistest"
	"github.com/ericthz/zebra/internal/tool"
)

// newVoiceServer 构造带语音能力的服务（语音网关 + Agent 均为本地 mock）。
func newVoiceServer(t *testing.T) (http.Handler, *httptest.Server) {
	t.Helper()
	// 本地 mock 语音网关（OpenAI 兼容）
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"北京今天天气怎么样？"}`)
	})
	mux.HandleFunc("/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "FAKE-MP3-DATA")
	})
	voiceMock := httptest.NewServer(mux)
	t.Cleanup(voiceMock.Close)

	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。角色：{role}。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "北京今天 25 度，晴朗。"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   NewInMemoryStore(time.Minute),
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
		Voice: &provider.VoiceClient{
			BaseURL: voiceMock.URL, ASRModel: "whisper-1", TTSModel: "tts-1",
			Voice: "alloy", Client: voiceMock.Client(),
		},
	})
	return api.Handler(), voiceMock
}

func multipartAudio(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	boundary := mw.Boundary()
	fw, _ := mw.CreateFormFile("file", "voice.wav")
	fw.Write([]byte("RIFF....WAV"))
	mw.Close()
	return &buf, boundary
}

func TestVoiceChatEndToEnd(t *testing.T) {
	h, _ := newVoiceServer(t)
	body, boundary := multipartAudio(t)
	req := httptest.NewRequest("POST", "/v1/voice/chat", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("voice/chat 应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}

	var resp VoiceChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "北京") || !strings.Contains(resp.Reply, "25 度") {
		t.Fatalf("文本链路异常: %+v", resp)
	}
	audio, err := base64.StdEncoding.DecodeString(resp.AudioBase64)
	if err != nil || string(audio) != "FAKE-MP3-DATA" {
		t.Fatalf("音频回传异常: %v %q", err, audio)
	}
	if resp.SessionID == "" {
		t.Fatal("应返回 session_id 支持多轮续接")
	}
}

func TestVoiceTranscribeAndSynthesize(t *testing.T) {
	h, _ := newVoiceServer(t)

	// ASR
	body, boundary := multipartAudio(t)
	req := httptest.NewRequest("POST", "/v1/voice/transcribe", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "北京") {
		t.Fatalf("transcribe 异常: %d %s", rr.Code, rr.Body.String())
	}

	// TTS
	req = httptest.NewRequest("POST", "/v1/voice/synthesize",
		bytes.NewBufferString(`{"text":"你好"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer user-key")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "FAKE-MP3-DATA" {
		t.Fatalf("synthesize 异常: %d %s", rr.Code, rr.Body.String())
	}
}

// TestVoiceAudioSizeLimit 验证：超大音频被拒绝（单缓冲流式读取）。
func TestVoiceAudioSizeLimit(t *testing.T) {
	h, _ := newVoiceServer(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	boundary := mw.Boundary()
	fw, _ := mw.CreateFormFile("file", "big.wav")
	fw.Write(make([]byte, 33<<20)) // 33MB > 32MB 上限
	mw.Close()

	req := httptest.NewRequest("POST", "/v1/voice/transcribe", &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("超大音频应 400，实际 %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestVoiceMissingFile 验证：缺 file 字段返回 400。
func TestVoiceMissingFile(t *testing.T) {
	h, _ := newVoiceServer(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	boundary := mw.Boundary()
	mw.WriteField("other", "x")
	mw.Close()

	req := httptest.NewRequest("POST", "/v1/voice/transcribe", &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("缺 file 应 400，实际 %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestVoiceChatPersistsHistory 验证：语音对话也把历史写回（Redis 会话下多轮不丢）。
func TestVoiceChatPersistsHistory(t *testing.T) {
	fs := redistest.New(t)
	redisStore := NewRedisSessionStore(&redis.Client{Addr: fs.Addr(), Timeout: 2 * time.Second}, 30*time.Minute)

	// 本地 mock 语音网关
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"北京今天天气怎么样？"}`)
	})
	mux.HandleFunc("/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "FAKE-MP3-DATA")
	})
	voiceMock := httptest.NewServer(mux)
	t.Cleanup(voiceMock.Close)

	keys := NewKeyStore()
	keys.Register(Principal{Key: "user-key", User: "alice", Role: "user", Tenant: "default"})
	prompts := prompt.NewRegistry("zebra")
	prompts.Register(&prompt.Template{Name: "assistant", Version: "v1", Text: "你是 zebra AI 助手。"})

	api := NewAPIServer(Deps{
		Router:     provider.NewRouter(&staticProvider{reply: "25 度，晴朗。"}),
		Tools:      tool.NewRegistry(),
		Prompts:    prompts,
		Sessions:   redisStore,
		Keys:       keys,
		Rate:       NewRateLimiter(100, 100),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    NewMetrics(),
		MaxTurns:   5,
		PromptName: "assistant",
		Voice: &provider.VoiceClient{
			BaseURL: voiceMock.URL, ASRModel: "whisper-1", TTSModel: "tts-1",
			Voice: "alloy", Client: voiceMock.Client(),
		},
	})
	h := api.Handler()

	body, boundary := multipartAudio(t)
	req := httptest.NewRequest("POST", "/v1/voice/chat", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer user-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("voice/chat 应 200，实际 %d body=%s", rr.Code, rr.Body.String())
	}

	// 历史应从 Redis 会话存储读回（persistHistory 已调用）
	var vcr VoiceChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &vcr); err != nil {
		t.Fatal(err)
	}
	if vcr.SessionID == "" {
		t.Fatal("应返回 session_id")
	}
	sess, ok := redisStore.Get(vcr.SessionID)
	if !ok {
		t.Fatal("会话应存在于 Redis 存储")
	}
	if len(*sess.History()) < 2 {
		t.Fatalf("语音对话后历史应写回（含 user 提问与 assistant 回复），实际 %d 条: %+v",
			len(*sess.History()), *sess.History())
	}
}

// TestReadAudioPartFieldOrder 验证：session_id 放在 file 字段之后也生效
// （readAudioPart 不得因先读到 file 就提前返回，否则后续字段被丢弃）。
func TestReadAudioPartFieldOrder(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("some_field", "ignored")
	fw, _ := mw.CreateFormFile("file", "voice.wav")
	fw.Write([]byte("RIFF....WAV"))
	mw.WriteField("session_id", "sess-after-file")
	mw.Close()

	req := httptest.NewRequest("POST", "/v1/voice/chat", &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+mw.Boundary())
	audio, fields, err := readAudioPart(req, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "RIFF....WAV" {
		t.Fatalf("音频内容应完整: %q", audio)
	}
	if fields["session_id"] != "sess-after-file" {
		t.Fatalf("file 之后的 session_id 字段应被解析生效，实际 %q", fields["session_id"])
	}
	if fields["some_field"] != "ignored" {
		t.Fatalf("file 之前的普通字段应保留，实际 %q", fields["some_field"])
	}
}
