package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ericthz/zebra/internal/tool"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRegisterToolsDisabled(t *testing.T) {
	t.Setenv("MCP_MODE", "")
	reg := tool.NewRegistry()
	mode, n := RegisterTools(reg, discardLogger())
	if mode != "" || n != 0 || len(reg.Names()) != 0 {
		t.Fatalf("未配置 MCP 应返回 (\"\", 0): mode=%q n=%d", mode, n)
	}
}

func TestRegisterToolsBadMode(t *testing.T) {
	t.Setenv("MCP_MODE", "weird")
	reg := tool.NewRegistry()
	_, n := RegisterTools(reg, discardLogger())
	if n != 0 {
		t.Fatalf("非法模式不应注册工具: %d", n)
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
	mode, n := RegisterTools(reg, discardLogger())
	if mode != "http" || n != 1 {
		t.Fatalf("应注册 1 个 MCP 工具: mode=%q n=%d", mode, n)
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "mcp_echo" {
		t.Fatalf("MCP 工具未注册进 registry: %v", names)
	}
}
