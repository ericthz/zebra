package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ericthz/zebra/internal/tool"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRegisterToolsDisabled(t *testing.T) {
	t.Setenv("MCP_MODE", "")
	reg := tool.NewRegistry()
	mode, defs := RegisterTools(reg, discardLogger())
	if mode != "" || len(defs) != 0 || len(reg.Names()) != 0 {
		t.Fatalf("未配置 MCP 应返回 (\"\", nil): mode=%q defs=%d", mode, len(defs))
	}
}

func TestRegisterToolsBadMode(t *testing.T) {
	t.Setenv("MCP_MODE", "weird")
	reg := tool.NewRegistry()
	_, defs := RegisterTools(reg, discardLogger())
	if len(defs) != 0 {
		t.Fatalf("非法模式不应注册工具: %d", len(defs))
	}
}

// mockMCPServer 最小 JSON-RPC MCP 服务器（initialize / tools/list）。
func mockMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result json.RawMessage
		switch req.Method {
		case "initialize":
			result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock"}}`)
		case "notifications/initialized":
			result = json.RawMessage(`{}`)
		case "tools/list":
			result = json.RawMessage(`{"tools":[{"name":"mcp_echo","description":"echo tool","inputSchema":{"type":"object"}}]}`)
		default:
			http.Error(w, "unknown method "+req.Method, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: result})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestRegisterToolsHTTP(t *testing.T) {
	ts := mockMCPServer(t)
	t.Setenv("MCP_MODE", "http")
	t.Setenv("MCP_HTTP_URL", ts.URL)

	reg := tool.NewRegistry()
	mode, defs := RegisterTools(reg, discardLogger())
	if mode != "http" || len(defs) != 1 {
		t.Fatalf("应注册 1 个 MCP 工具: mode=%q defs=%d", mode, len(defs))
	}
	if defs[0].Name != "mcp_echo" || defs[0].Description != "echo tool" {
		t.Fatalf("MCP 定义返回异常: %+v", defs[0])
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "mcp_echo" {
		t.Fatalf("MCP 工具未注册进 registry: %v", names)
	}
}

// TestValidateHTTPStart P2-E：HTTP 模式未配置令牌必须拒绝启动（默认拒绝）。
func TestValidateHTTPStart(t *testing.T) {
	// HTTP 模式 + 空令牌 → 拒绝
	if err := ValidateHTTPStart(":9000", ""); err == nil {
		t.Fatal("HTTP 模式空令牌应拒绝启动")
	}
	// HTTP 模式 + 有令牌 → 放行
	if err := ValidateHTTPStart(":9000", "secret"); err != nil {
		t.Fatalf("HTTP 模式带令牌应放行: %v", err)
	}
	// stdio 模式无令牌 → 放行（本地管道由启动方授权）
	if err := ValidateHTTPStart("", ""); err != nil {
		t.Fatalf("stdio 模式无需令牌: %v", err)
	}
}

// TestHTTPAuth 验证：HTTP 模式设置了 token 后，未带/带错 Bearer 的请求被 401 拒绝。
func TestHTTPAuth(t *testing.T) {
	srv := NewServer(discardLogger())
	srv.RegisterTool(ToolDef{Name: "ping"}, func(ctx context.Context, args map[string]interface{}) (*CallToolResult, error) {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: "pong"}}}, nil
	})

	// 未启用鉴权：放行
	muxNoAuth := http.NewServeMux()
	muxNoAuth.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		req := &Request{JSONRPC: "2.0", Method: "tools/call", ID: 1, Params: json.RawMessage(`{"name":"ping"}`)}
		resp := srv.process(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	tsNoAuth := httptest.NewServer(muxNoAuth)
	defer tsNoAuth.Close()

	// 启用鉴权后：缺 token / 错 token → 401；正确 token → 放行
	srv.SetHTTPToken("secret-token")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !srv.checkToken(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		req := &Request{JSONRPC: "2.0", Method: "initialize", ID: 1}
		resp := srv.process(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	doPost := func(url string, hdr map[string]string) (*http.Response, error) {
		body := strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1}`)
		req, _ := http.NewRequest(http.MethodPost, url, body)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		return http.DefaultClient.Do(req)
	}

	if resp, err := doPost(ts.URL, nil); err != nil {
		t.Fatal(err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("无 token 应 401，实际 %d", resp.StatusCode)
		}
	}
	if resp, err := doPost(ts.URL, map[string]string{"Authorization": "Bearer wrong"}); err != nil {
		t.Fatal(err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("错误 token 应 401，实际 %d", resp.StatusCode)
		}
	}
	if resp, err := doPost(ts.URL, map[string]string{"Authorization": "Bearer secret-token"}); err != nil {
		t.Fatal(err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("正确 token 应放行，实际 %d", resp.StatusCode)
		}
	}
}
